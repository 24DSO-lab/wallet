package eth

import (
	"context"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"math/big"
	"net/http"
	"strings"
	"unicode"

	"github.com/24DSO-lab/wallet/internal/domain"
	"github.com/24DSO-lab/wallet/internal/provider"
)

// RPC это узкий интерфейс JSON-RPC Ethereum, нужный адаптеру. Тесты
// подставляют записанные ответы, боевая реализация это JSONRPC.
//
// Данные провайдера не доверенные: всё, что влияет на подпись, адаптер
// проверяет локально (Verify), а чужой chainId отсекает ensureChain.
type RPC interface {
	ChainID(ctx context.Context) (uint64, error)
	BlockNumber(ctx context.Context) (uint64, error)
	// Balance возвращает баланс ETH в wei на последнем блоке.
	Balance(ctx context.Context, addr Address) (*big.Int, error)
	// Call выполняет eth_call на последнем блоке.
	Call(ctx context.Context, to Address, data []byte) ([]byte, error)
	// PendingNonce возвращает nonce с учётом ожидающих транзакций.
	PendingNonce(ctx context.Context, addr Address) (uint64, error)
	EstimateGas(ctx context.Context, msg CallMsg) (uint64, error)
	FeeHistory(ctx context.Context, blocks int, percentiles []float64) (FeeHistory, error)
	// SendRawTransaction отправляет транзакцию и возвращает хэш, назвавший
	// провайдер. Повторов нет: после обрыва неизвестно, дошёл ли запрос.
	// Ответ «уже известна» приходит как ErrAlreadyKnown.
	SendRawTransaction(ctx context.Context, raw []byte) (string, error)
	// TransactionReceipt возвращает чек или nil, пока транзакции нет в блоке.
	TransactionReceipt(ctx context.Context, hash string) (*Receipt, error)
	// TransactionKnown сообщает, знает ли узел транзакцию (в блоке или в пуле).
	TransactionKnown(ctx context.Context, hash string) (bool, error)
}

// CallMsg это параметры eth_call и eth_estimateGas.
type CallMsg struct {
	From  Address
	To    Address
	Value *big.Int // nil означает ноль
	Data  []byte
}

// FeeHistory это ответ eth_feeHistory. BaseFee содержит на одну запись больше,
// чем блоков: последняя это базовая комиссия следующего блока.
type FeeHistory struct {
	OldestBlock uint64
	BaseFee     []*big.Int
	Reward      [][]*big.Int // по блокам, по запрошенным перцентилям
}

// Receipt это чек транзакции в блоке.
type Receipt struct {
	Status      uint64 // 1 успех, 0 откат
	BlockNumber uint64
}

// Ошибки RPC.
var (
	// ErrAlreadyKnown: узел уже знает эту транзакцию. Для повторной отправки
	// той же подписанной транзакции это успех.
	ErrAlreadyKnown = errors.New("eth: transaction is already known to the node")
	errBadQuantity  = errors.New("eth: malformed hex quantity")
	errBadData      = errors.New("eth: malformed hex data")
)

// RPCError это ошибка уровня JSON-RPC.
type RPCError struct {
	Code    int
	Message string
}

func (e *RPCError) Error() string { return fmt.Sprintf("rpc error %d: %s", e.Code, e.Message) }

func (e *RPCError) rateLimited() bool {
	if e.Code == -32005 || e.Code == 429 {
		return true
	}
	return containsFold(e.Message, "rate limit", "too many requests", "limit exceeded", "capacity exceeded")
}

func (e *RPCError) alreadyKnown() bool {
	return containsFold(e.Message, "already known", "known transaction", "already imported")
}

func containsFold(s string, subs ...string) bool {
	s = strings.ToLower(s)
	for _, sub := range subs {
		if strings.Contains(s, sub) {
			return true
		}
	}
	return false
}

// cleanMessage ограничивает текст ошибки провайдера: он приходит извне и
// попадает в логи.
func cleanMessage(s string) string {
	const limit = 200
	out := make([]rune, 0, min(len(s), limit))
	for _, r := range s {
		if len(out) == limit {
			break
		}
		if unicode.IsControl(r) {
			r = ' '
		}
		out = append(out, r)
	}
	return string(out)
}

// JSONRPC это реализация RPC поверх provider.Client. Базовый адрес клиента
// это адрес RPC-провайдера целиком (ключ Alchemy входит в адрес и должен быть
// зарегистрирован в logging.Redactor клиента).
type JSONRPC struct {
	client provider.Client
}

var _ RPC = (*JSONRPC)(nil)

// NewJSONRPC создаёт клиент. http.Client здесь не создаётся.
func NewJSONRPC(client provider.Client) (*JSONRPC, error) {
	if client == nil {
		return nil, errors.New("eth: provider client is nil")
	}
	return &JSONRPC{client: client}, nil
}

type rpcRequest struct {
	JSONRPC string `json:"jsonrpc"`
	ID      int    `json:"id"`
	Method  string `json:"method"`
	Params  []any  `json:"params"`
}

type rpcResponse struct {
	ID     json.RawMessage `json:"id"`
	Result json.RawMessage `json:"result"`
	Error  *struct {
		Code    int    `json:"code"`
		Message string `json:"message"`
	} `json:"error"`
}

var jsonHeader = http.Header{"Content-Type": {"application/json"}}

func badResponse(err error) error {
	return domain.NewError(domain.CodeProviderBadResponse, err)
}

// call выполняет один запрос. Ошибка уровня JSON-RPC возвращается как *RPCError,
// остальные приходят уже с кодом от provider.Client.
func (c *JSONRPC) call(ctx context.Context, idempotent bool, method string, params ...any) (json.RawMessage, error) {
	body, err := json.Marshal(rpcRequest{JSONRPC: "2.0", ID: 1, Method: method, Params: params})
	if err != nil {
		return nil, domain.NewError(domain.CodeInternal, err)
	}
	resp, err := c.client.Do(ctx, provider.Request{
		Method:     http.MethodPost,
		Header:     jsonHeader,
		Body:       body,
		Idempotent: idempotent,
	})
	if err != nil {
		return nil, err
	}
	var out rpcResponse
	if err := json.Unmarshal(resp.Body, &out); err != nil {
		return nil, badResponse(errors.New("eth: response is not JSON-RPC"))
	}
	if out.Error != nil {
		return nil, &RPCError{Code: out.Error.Code, Message: cleanMessage(out.Error.Message)}
	}
	if string(out.ID) != "1" || len(out.Result) == 0 {
		return nil, badResponse(errors.New("eth: response has no result or a foreign id"))
	}
	return out.Result, nil
}

// read это идемпотентный запрос чтения: повторы и резервные адреса разрешены.
func (c *JSONRPC) read(ctx context.Context, method string, params ...any) (json.RawMessage, error) {
	raw, err := c.call(ctx, true, method, params...)
	var re *RPCError
	if errors.As(err, &re) {
		if re.rateLimited() {
			return nil, domain.NewError(domain.CodeProviderRateLimited, re)
		}
		return nil, badResponse(re)
	}
	return raw, err
}

func (c *JSONRPC) readQuantity(ctx context.Context, method string, params ...any) (*big.Int, error) {
	raw, err := c.read(ctx, method, params...)
	if err != nil {
		return nil, err
	}
	var s string
	if err := json.Unmarshal(raw, &s); err != nil {
		return nil, badResponse(errBadQuantity)
	}
	n, err := parseQuantity(s)
	if err != nil {
		return nil, badResponse(err)
	}
	return n, nil
}

func (c *JSONRPC) readUint64(ctx context.Context, method string, params ...any) (uint64, error) {
	n, err := c.readQuantity(ctx, method, params...)
	if err != nil {
		return 0, err
	}
	if !n.IsUint64() {
		return 0, badResponse(errBadQuantity)
	}
	return n.Uint64(), nil
}

// ChainID возвращает eth_chainId.
func (c *JSONRPC) ChainID(ctx context.Context) (uint64, error) {
	return c.readUint64(ctx, "eth_chainId")
}

// BlockNumber возвращает номер последнего блока.
func (c *JSONRPC) BlockNumber(ctx context.Context) (uint64, error) {
	return c.readUint64(ctx, "eth_blockNumber")
}

// Balance возвращает баланс адреса в wei.
func (c *JSONRPC) Balance(ctx context.Context, addr Address) (*big.Int, error) {
	return c.readQuantity(ctx, "eth_getBalance", lowerHex(addr), "latest")
}

// PendingNonce возвращает nonce с учётом ожидающих транзакций.
func (c *JSONRPC) PendingNonce(ctx context.Context, addr Address) (uint64, error) {
	return c.readUint64(ctx, "eth_getTransactionCount", lowerHex(addr), "pending")
}

// Call выполняет eth_call.
func (c *JSONRPC) Call(ctx context.Context, to Address, data []byte) ([]byte, error) {
	msg := map[string]string{"to": lowerHex(to), "data": "0x" + hex.EncodeToString(data)}
	raw, err := c.read(ctx, "eth_call", msg, "latest")
	if err != nil {
		return nil, err
	}
	var s string
	if err := json.Unmarshal(raw, &s); err != nil {
		return nil, badResponse(errBadData)
	}
	out, err := parseHexData(s)
	if err != nil {
		return nil, badResponse(err)
	}
	return out, nil
}

// EstimateGas возвращает оценку газа узла.
func (c *JSONRPC) EstimateGas(ctx context.Context, m CallMsg) (uint64, error) {
	msg := map[string]string{"from": lowerHex(m.From), "to": lowerHex(m.To)}
	if m.Value != nil && m.Value.Sign() > 0 {
		msg["value"] = hexQuantity(m.Value)
	}
	if len(m.Data) > 0 {
		msg["data"] = "0x" + hex.EncodeToString(m.Data)
	}
	return c.readUint64(ctx, "eth_estimateGas", msg)
}

// maxFeeHistoryBlocks ограничивает ответ eth_feeHistory: провайдер не доверенный.
const maxFeeHistoryBlocks = 1024

// FeeHistory возвращает историю комиссий последних блоков.
func (c *JSONRPC) FeeHistory(ctx context.Context, blocks int, percentiles []float64) (FeeHistory, error) {
	if blocks < 1 || blocks > maxFeeHistoryBlocks {
		return FeeHistory{}, domain.NewError(domain.CodeInternal, errors.New("eth: fee history block count out of range"))
	}
	raw, err := c.read(ctx, "eth_feeHistory", hexQuantity(big.NewInt(int64(blocks))), "latest", percentiles)
	if err != nil {
		return FeeHistory{}, err
	}
	var r struct {
		OldestBlock   string     `json:"oldestBlock"`
		BaseFeePerGas []string   `json:"baseFeePerGas"`
		Reward        [][]string `json:"reward"`
	}
	if err := json.Unmarshal(raw, &r); err != nil {
		return FeeHistory{}, badResponse(errors.New("eth: malformed fee history"))
	}
	if len(r.BaseFeePerGas) == 0 || len(r.BaseFeePerGas) > maxFeeHistoryBlocks+1 || len(r.Reward) > maxFeeHistoryBlocks {
		return FeeHistory{}, badResponse(errors.New("eth: fee history has an unexpected size"))
	}
	var fh FeeHistory
	if fh.OldestBlock, err = parseUint64Quantity(r.OldestBlock); err != nil {
		return FeeHistory{}, badResponse(err)
	}
	for _, s := range r.BaseFeePerGas {
		n, err := parseQuantity(s)
		if err != nil {
			return FeeHistory{}, badResponse(err)
		}
		fh.BaseFee = append(fh.BaseFee, n)
	}
	for _, row := range r.Reward {
		if len(row) != len(percentiles) {
			return FeeHistory{}, badResponse(errors.New("eth: fee history reward row has an unexpected size"))
		}
		parsed := make([]*big.Int, 0, len(row))
		for _, s := range row {
			n, err := parseQuantity(s)
			if err != nil {
				return FeeHistory{}, badResponse(err)
			}
			parsed = append(parsed, n)
		}
		fh.Reward = append(fh.Reward, parsed)
	}
	return fh, nil
}

// SendRawTransaction отправляет транзакцию. Запрос не идемпотентный по
// определению клиента: повторов и резервных адресов нет.
func (c *JSONRPC) SendRawTransaction(ctx context.Context, raw []byte) (string, error) {
	res, err := c.call(ctx, false, "eth_sendRawTransaction", "0x"+hex.EncodeToString(raw))
	var re *RPCError
	switch {
	case errors.As(err, &re):
		switch {
		case re.alreadyKnown():
			return "", fmt.Errorf("%w: %w", ErrAlreadyKnown, re)
		case re.rateLimited():
			return "", domain.NewError(domain.CodeProviderRateLimited, re)
		}
		return "", domain.NewError(domain.CodeBroadcastRejected, re)
	case err != nil:
		return "", err
	}
	var h string
	if err := json.Unmarshal(res, &h); err != nil {
		return "", badResponse(errors.New("eth: malformed transaction hash"))
	}
	return h, nil
}

// TransactionReceipt возвращает чек или nil, если транзакция ещё не в блоке.
func (c *JSONRPC) TransactionReceipt(ctx context.Context, hash string) (*Receipt, error) {
	raw, err := c.read(ctx, "eth_getTransactionReceipt", hash)
	if err != nil {
		return nil, err
	}
	if string(raw) == "null" {
		return nil, nil
	}
	var r struct {
		Status      string `json:"status"`
		BlockNumber string `json:"blockNumber"`
	}
	if err := json.Unmarshal(raw, &r); err != nil {
		return nil, badResponse(errors.New("eth: malformed receipt"))
	}
	status, err := parseUint64Quantity(r.Status)
	if err != nil || status > 1 {
		return nil, badResponse(errors.New("eth: receipt has no valid status"))
	}
	block, err := parseUint64Quantity(r.BlockNumber)
	if err != nil {
		return nil, badResponse(err)
	}
	return &Receipt{Status: status, BlockNumber: block}, nil
}

// TransactionKnown сообщает, нашёл ли узел транзакцию по хэшу.
func (c *JSONRPC) TransactionKnown(ctx context.Context, hash string) (bool, error) {
	raw, err := c.read(ctx, "eth_getTransactionByHash", hash)
	if err != nil {
		return false, err
	}
	return string(raw) != "null", nil
}

func lowerHex(a Address) string { return "0x" + hex.EncodeToString(a[:]) }

// hexQuantity кодирует число как QUANTITY JSON-RPC: 0x без ведущих нулей.
func hexQuantity(n *big.Int) string { return "0x" + n.Text(16) }

// parseQuantity разбирает QUANTITY: 0x и от 1 до 64 hex-цифр (до 256 бит).
func parseQuantity(s string) (*big.Int, error) {
	if len(s) < 3 || len(s) > 2+64 || s[0] != '0' || s[1] != 'x' {
		return nil, errBadQuantity
	}
	for i := 2; i < len(s); i++ {
		c := s[i]
		if (c < '0' || c > '9') && (c < 'a' || c > 'f') && (c < 'A' || c > 'F') {
			return nil, errBadQuantity
		}
	}
	n, ok := new(big.Int).SetString(s[2:], 16)
	if !ok {
		return nil, errBadQuantity
	}
	return n, nil
}

func parseUint64Quantity(s string) (uint64, error) {
	n, err := parseQuantity(s)
	if err != nil || !n.IsUint64() {
		return 0, errBadQuantity
	}
	return n.Uint64(), nil
}

// parseHexData разбирает DATA: 0x и чётное число hex-цифр (возможно ноль).
func parseHexData(s string) ([]byte, error) {
	if len(s) < 2 || s[0] != '0' || s[1] != 'x' {
		return nil, errBadData
	}
	b, err := hex.DecodeString(s[2:])
	if err != nil {
		return nil, errBadData
	}
	return b, nil
}
