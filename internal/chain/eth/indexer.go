package eth

import (
	"context"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"math/big"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/24DSO-lab/wallet/internal/domain"
	"github.com/24DSO-lab/wallet/internal/provider"
)

// Indexer это источник истории адреса (Etherscan). Адаптер видит только
// разобранные записи: тесты идут на записанных ответах или фейках, а не на
// живом API. Записи из внешнего источника проверяются здесь и при склейке.
type Indexer interface {
	// NormalTxs возвращает обычные транзакции адреса, новые первыми.
	NormalTxs(ctx context.Context, addr Address) ([]IndexedTx, error)
	// TokenTransfers возвращает переводы токена contract с участием адреса,
	// новые первыми.
	TokenTransfers(ctx context.Context, addr, contract Address) ([]IndexedTransfer, error)
}

// IndexedTx это обычная транзакция из индексатора.
type IndexedTx struct {
	Hash     string // 0x и 64 hex-цифры в нижнем регистре
	From, To Address
	HasTo    bool // false при создании контракта
	Value    *big.Int
	Failed   bool
	HasInput bool   // у транзакции есть calldata (это вызов контракта)
	Input    []byte // calldata, только если ровно 68 байт (форма transfer), иначе nil
	Block    uint64
	Time     time.Time
	GasUsed  uint64
	GasPrice *big.Int // фактическая цена газа
}

// IndexedTransfer это событие Transfer токена из индексатора.
type IndexedTransfer struct {
	Hash     string
	Contract Address
	From, To Address
	Value    *big.Int
	Block    uint64
	Time     time.Time
}

// Etherscan это Indexer поверх Etherscan API V2. Базовый адрес клиента это
// адрес конечной точки целиком (https://api.etherscan.io/v2/api), сеть
// выбирается параметром chainid.
type Etherscan struct {
	client   provider.Client
	chainID  uint64
	mu       sync.RWMutex
	apiKey   string
	pageSize int
	maxPages int
}

var _ Indexer = (*Etherscan)(nil)

// Размер страницы и число страниц ограничивают объём истории: 1000 самых
// новых записей каждого вида. Старее них кэш Store хранит прежние записи.
const (
	etherscanPageSize = 100
	etherscanMaxPages = 10
)

// NewEtherscan создаёт индексатор. Ключ API может быть пуст (тариф без ключа
// с жёсткими лимитами); он уходит в запрос и должен быть зарегистрирован в
// logging.Redactor клиента.
func NewEtherscan(client provider.Client, chainID uint64, apiKey string) (*Etherscan, error) {
	if client == nil {
		return nil, errors.New("eth: provider client is nil")
	}
	return &Etherscan{client: client, chainID: chainID, apiKey: apiKey, pageSize: etherscanPageSize, maxPages: etherscanMaxPages}, nil
}

// SetAPIKey updates the optional Etherscan credential without rebuilding the
// profile. Each history request snapshots one key so pagination uses it consistently.
func (e *Etherscan) SetAPIKey(key string) {
	e.mu.Lock()
	e.apiKey = key
	e.mu.Unlock()
}

// rows запрашивает страницы, пока они полные, и возвращает строки как есть.
func (e *Etherscan) rows(ctx context.Context, action string, extra url.Values) ([]json.RawMessage, error) {
	e.mu.RLock()
	apiKey := e.apiKey
	e.mu.RUnlock()
	var all []json.RawMessage
	for page := 1; page <= e.maxPages; page++ {
		q := url.Values{
			"chainid":    {strconv.FormatUint(e.chainID, 10)},
			"module":     {"account"},
			"action":     {action},
			"startblock": {"0"},
			"endblock":   {"99999999"},
			"page":       {strconv.Itoa(page)},
			"offset":     {strconv.Itoa(e.pageSize)},
			"sort":       {"desc"},
		}
		for k, v := range extra {
			q[k] = v
		}
		if apiKey != "" {
			q.Set("apikey", apiKey)
		}
		resp, err := e.client.Do(ctx, provider.Request{Query: q})
		if err != nil {
			return nil, err
		}
		got, err := decodeEnvelope(resp.Body)
		if err != nil {
			return nil, err
		}
		all = append(all, got...)
		if len(got) < e.pageSize {
			break
		}
		if page == e.maxPages {
			return nil, badResponse(errors.New("eth: indexer history exceeds the configured page limit"))
		}
	}
	return all, nil
}

// decodeEnvelope разбирает конверт Etherscan. Пустой результат приходит как
// status 0 с сообщением «No transactions found», лимит частоты как status 0
// и текст в result при HTTP 200.
func decodeEnvelope(body []byte) ([]json.RawMessage, error) {
	var env struct {
		Status  string          `json:"status"`
		Message string          `json:"message"`
		Result  json.RawMessage `json:"result"`
	}
	if err := json.Unmarshal(body, &env); err != nil {
		return nil, badResponse(errors.New("eth: indexer response is not JSON"))
	}
	if env.Status == "1" {
		var rows []json.RawMessage
		if err := json.Unmarshal(env.Result, &rows); err != nil {
			return nil, badResponse(errors.New("eth: indexer result is not a list"))
		}
		return rows, nil
	}
	var text string
	if err := json.Unmarshal(env.Result, &text); err != nil || text == "" {
		text = env.Message
	}
	switch {
	case containsFold(env.Message, "no transactions found") || containsFold(env.Message, "no records found"):
		return nil, nil
	case containsFold(text, "rate limit", "max calls per sec"):
		return nil, domain.NewError(domain.CodeProviderRateLimited, errors.New("eth: indexer: "+cleanMessage(text)))
	}
	return nil, badResponse(errors.New("eth: indexer: " + cleanMessage(text)))
}

type etherscanTx struct {
	BlockNumber     string `json:"blockNumber"`
	TimeStamp       string `json:"timeStamp"`
	Hash            string `json:"hash"`
	From            string `json:"from"`
	To              string `json:"to"`
	Value           string `json:"value"`
	IsError         string `json:"isError"`
	TxReceiptStatus string `json:"txreceipt_status"`
	Input           string `json:"input"`
	GasUsed         string `json:"gasUsed"`
	GasPrice        string `json:"gasPrice"`
}

// NormalTxs возвращает обычные транзакции адреса.
func (e *Etherscan) NormalTxs(ctx context.Context, addr Address) ([]IndexedTx, error) {
	raw, err := e.rows(ctx, "txlist", url.Values{"address": {lowerHex(addr)}})
	if err != nil {
		return nil, err
	}
	out := make([]IndexedTx, 0, len(raw))
	for _, r := range raw {
		var row etherscanTx
		if err := json.Unmarshal(r, &row); err != nil {
			return nil, badResponse(errors.New("eth: malformed transaction row"))
		}
		tx, err := row.decode()
		if err != nil {
			return nil, badResponse(err)
		}
		out = append(out, tx)
	}
	return out, nil
}

func (r etherscanTx) decode() (IndexedTx, error) {
	var (
		tx  IndexedTx
		err error
	)
	if tx.Hash, err = parseTxHash(r.Hash); err != nil {
		return tx, err
	}
	if tx.From, err = parseRowAddress(r.From); err != nil {
		return tx, err
	}
	if r.To != "" {
		if tx.To, err = parseRowAddress(r.To); err != nil {
			return tx, err
		}
		tx.HasTo = true
	}
	if tx.Value, err = parseDecimal(r.Value); err != nil {
		return tx, err
	}
	if tx.GasPrice, err = parseDecimal(r.GasPrice); err != nil {
		return tx, err
	}
	if tx.GasUsed, err = parseUintDecimal(r.GasUsed); err != nil {
		return tx, err
	}
	if tx.Block, err = parseUintDecimal(r.BlockNumber); err != nil {
		return tx, err
	}
	if tx.Time, err = parseTimestamp(r.TimeStamp); err != nil {
		return tx, err
	}
	if (r.IsError != "0" && r.IsError != "1") || (r.TxReceiptStatus != "0" && r.TxReceiptStatus != "1") {
		return tx, errBadRow
	}
	tx.Failed = r.IsError == "1" || r.TxReceiptStatus == "0"
	input, err := parseHexData(r.Input)
	if err != nil {
		return tx, err
	}
	tx.HasInput = len(input) > 0
	if len(input) == transferCallLen {
		tx.Input = input
	}
	return tx, nil
}

type etherscanTransfer struct {
	BlockNumber     string `json:"blockNumber"`
	TimeStamp       string `json:"timeStamp"`
	Hash            string `json:"hash"`
	From            string `json:"from"`
	To              string `json:"to"`
	Value           string `json:"value"`
	ContractAddress string `json:"contractAddress"`
}

// TokenTransfers возвращает переводы токена contract с участием адреса.
func (e *Etherscan) TokenTransfers(ctx context.Context, addr, contract Address) ([]IndexedTransfer, error) {
	raw, err := e.rows(ctx, "tokentx", url.Values{"address": {lowerHex(addr)}, "contractaddress": {lowerHex(contract)}})
	if err != nil {
		return nil, err
	}
	out := make([]IndexedTransfer, 0, len(raw))
	for _, r := range raw {
		var row etherscanTransfer
		if err := json.Unmarshal(r, &row); err != nil {
			return nil, badResponse(errors.New("eth: malformed transfer row"))
		}
		tr, err := row.decode()
		if err != nil {
			return nil, badResponse(err)
		}
		out = append(out, tr)
	}
	return out, nil
}

func (r etherscanTransfer) decode() (IndexedTransfer, error) {
	var (
		tr  IndexedTransfer
		err error
	)
	if tr.Hash, err = parseTxHash(r.Hash); err != nil {
		return tr, err
	}
	if tr.Contract, err = parseRowAddress(r.ContractAddress); err != nil {
		return tr, err
	}
	if tr.From, err = parseRowAddress(r.From); err != nil {
		return tr, err
	}
	if tr.To, err = parseRowAddress(r.To); err != nil {
		return tr, err
	}
	if tr.Value, err = parseDecimal(r.Value); err != nil {
		return tr, err
	}
	if tr.Block, err = parseUintDecimal(r.BlockNumber); err != nil {
		return tr, err
	}
	tr.Time, err = parseTimestamp(r.TimeStamp)
	return tr, err
}

var errBadRow = errors.New("eth: malformed value in an indexer row")

func parseRowAddress(s string) (Address, error) {
	a, _, err := parseAddress(s)
	if err != nil {
		return a, errBadRow
	}
	return a, nil
}

// parseTxHash принимает 0x и 64 hex-цифры и возвращает форму в нижнем регистре.
func parseTxHash(s string) (string, error) {
	if len(s) != 2+64 || s[:2] != "0x" {
		return "", errBadRow
	}
	b, err := hex.DecodeString(s[2:])
	if err != nil || len(b) != 32 {
		return "", errBadRow
	}
	return "0x" + hex.EncodeToString(b), nil
}

// parseDecimal разбирает неотрицательное десятичное число до 256 бит.
func parseDecimal(s string) (*big.Int, error) {
	if s == "" || len(s) > 78 || strings.Trim(s, "0123456789") != "" {
		return nil, errBadRow
	}
	n, ok := new(big.Int).SetString(s, 10)
	if !ok || n.Cmp(maxUint256) > 0 {
		return nil, errBadRow
	}
	return n, nil
}

func parseUintDecimal(s string) (uint64, error) {
	n, err := parseDecimal(s)
	if err != nil || !n.IsUint64() {
		return 0, errBadRow
	}
	return n.Uint64(), nil
}

// maxUnixSeconds это 31 декабря 9999 года: всё, что позже, считается мусором.
const maxUnixSeconds = 253402300799

func parseTimestamp(s string) (time.Time, error) {
	sec, err := parseUintDecimal(s)
	if err != nil || sec > maxUnixSeconds {
		return time.Time{}, fmt.Errorf("%w: timestamp", errBadRow)
	}
	return time.Unix(int64(sec), 0).UTC(), nil
}
