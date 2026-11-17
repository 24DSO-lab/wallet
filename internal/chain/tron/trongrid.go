package tron

import (
	"context"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"math/big"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/24DSO-lab/wallet/internal/domain"
	"github.com/24DSO-lab/wallet/internal/provider"
)

// ErrBadResponse это общий корень ошибок разбора ответов TronGrid. Тексты
// ответов сервера в ошибки не попадают.
var ErrBadResponse = errors.New("tron: unexpected provider response")

// TronGrid реализует API поверх provider.Client. Ключ API (заголовок
// TRON-PRO-API-KEY) задаётся при создании клиента в provider.Options.Header
// и сюда не приходит.
type TronGrid struct {
	c provider.Client
}

var _ API = (*TronGrid)(nil)

// NewTronGrid создаёт клиент TronGrid.
func NewTronGrid(c provider.Client) (*TronGrid, error) {
	if c == nil {
		return nil, errors.New("tron: provider client is nil")
	}
	return &TronGrid{c: c}, nil
}

func bad(what string) error {
	return domain.NewError(domain.CodeProviderBadResponse, fmt.Errorf("%w: %s", ErrBadResponse, what))
}

// post отправляет JSON и разбирает ответ. idem разрешает повторы (чтение).
func (g *TronGrid) post(ctx context.Context, path string, body, out any, idem bool) error {
	b, err := json.Marshal(body)
	if err != nil {
		return domain.NewError(domain.CodeInternal, err)
	}
	resp, err := g.c.Do(ctx, provider.Request{
		Method: http.MethodPost, Path: path, Body: b, Idempotent: idem,
		Header: http.Header{"Content-Type": {"application/json"}, "Accept": {"application/json"}},
	})
	if err != nil {
		return mapProviderErr(resp, err)
	}
	return decode(resp.Body, out)
}

func (g *TronGrid) get(ctx context.Context, path string, q url.Values, out any) error {
	resp, err := g.c.Do(ctx, provider.Request{Method: http.MethodGet, Path: path, Query: q,
		Header: http.Header{"Accept": {"application/json"}}})
	if err != nil {
		return mapProviderErr(resp, err)
	}
	return decode(resp.Body, out)
}

// mapProviderErr: TronGrid отвечает на превышение частоты не только 429, но и
// 403 с текстом о лимите (штрафное окно около 30 с) и 503. Такой отказ это
// ограничение частоты, а не плохой ответ: интерфейс покажет другой код.
func mapProviderErr(resp *provider.Response, err error) error {
	if resp != nil && resp.Status == http.StatusForbidden &&
		strings.Contains(strings.ToLower(string(resp.Body)), "frequency limit") {
		return domain.NewError(domain.CodeProviderRateLimited, errors.New("tron: provider rate limit (HTTP 403)"))
	}
	return err
}

func decode(body []byte, out any) error {
	var probe struct {
		Error string `json:"Error"`
	}
	if json.Unmarshal(body, &probe) == nil && probe.Error != "" {
		// TronGrid отвечает 200 с {"Error": ...} на неверный запрос.
		return bad("provider reported an error")
	}
	if err := json.Unmarshal(body, out); err != nil {
		return bad("invalid JSON")
	}
	return nil
}

func decodeHex(s string, n int) ([]byte, error) {
	b, err := hex.DecodeString(s)
	if err != nil || (n >= 0 && len(b) != n) {
		return nil, bad("invalid hex field")
	}
	return b, nil
}

// NowBlock: POST /wallet/getnowblock.
func (g *TronGrid) NowBlock(ctx context.Context) (BlockRef, error) {
	var r struct {
		BlockID     string `json:"blockID"`
		BlockHeader struct {
			RawData struct {
				Number    uint64 `json:"number"`
				Timestamp int64  `json:"timestamp"`
			} `json:"raw_data"`
		} `json:"block_header"`
	}
	if err := g.post(ctx, "/wallet/getnowblock", struct{}{}, &r, true); err != nil {
		return BlockRef{}, err
	}
	id, err := decodeHex(r.BlockID, 32)
	if err != nil {
		return BlockRef{}, err
	}
	n := r.BlockHeader.RawData.Number
	// blockID начинается с номера блока: расхождение значит порченый ответ.
	if n == 0 || binary.BigEndian.Uint64(id[:8]) != n || r.BlockHeader.RawData.Timestamp <= 0 {
		return BlockRef{}, bad("block id does not match block number")
	}
	return BlockRef{Number: n, ID: [32]byte(id), Timestamp: time.UnixMilli(r.BlockHeader.RawData.Timestamp)}, nil
}

// Account: GET /v1/accounts/{address}.
func (g *TronGrid) Account(ctx context.Context, owner Addr) (uint64, bool, error) {
	var r struct {
		Data []struct {
			Balance uint64 `json:"balance"`
		} `json:"data"`
	}
	if err := g.get(ctx, "/v1/accounts/"+owner.String(), nil, &r); err != nil {
		return 0, false, err
	}
	if len(r.Data) == 0 {
		return 0, false, nil // аккаунт ещё не активирован
	}
	return r.Data[0].Balance, true, nil
}

// Resources: POST /wallet/getaccountresource.
func (g *TronGrid) Resources(ctx context.Context, owner Addr) (Resources, error) {
	var r struct {
		FreeNetLimit uint64 `json:"freeNetLimit"`
		FreeNetUsed  uint64 `json:"freeNetUsed"`
		NetLimit     uint64 `json:"NetLimit"`
		NetUsed      uint64 `json:"NetUsed"`
		EnergyLimit  uint64 `json:"EnergyLimit"`
		EnergyUsed   uint64 `json:"EnergyUsed"`
	}
	req := map[string]any{"address": owner.String(), "visible": true}
	if err := g.post(ctx, "/wallet/getaccountresource", req, &r, true); err != nil {
		return Resources{}, err
	}
	left := func(limit, used uint64) uint64 {
		if used >= limit {
			return 0
		}
		return limit - used
	}
	return Resources{
		Bandwidth: left(r.FreeNetLimit, r.FreeNetUsed) + left(r.NetLimit, r.NetUsed),
		Energy:    left(r.EnergyLimit, r.EnergyUsed),
	}, nil
}

type constantResp struct {
	Result struct {
		Result bool `json:"result"`
	} `json:"result"`
	EnergyUsed     uint64   `json:"energy_used"`
	ConstantResult []string `json:"constant_result"`
}

func (g *TronGrid) constant(ctx context.Context, owner, contract Addr, selector string, param []byte) (constantResp, error) {
	var r constantResp
	req := map[string]any{
		"owner_address": owner.String(), "contract_address": contract.String(),
		"function_selector": selector, "parameter": hex.EncodeToString(param), "visible": true,
	}
	if err := g.post(ctx, "/wallet/triggerconstantcontract", req, &r, true); err != nil {
		return r, err
	}
	if !r.Result.Result {
		return r, bad("constant call failed")
	}
	return r, nil
}

// TRC20Balance: balanceOf(address) через triggerconstantcontract.
func (g *TronGrid) TRC20Balance(ctx context.Context, contract, owner Addr) (*big.Int, error) {
	param := make([]byte, 32)
	copy(param[12:], owner[1:])
	r, err := g.constant(ctx, owner, contract, "balanceOf(address)", param)
	if err != nil {
		return nil, err
	}
	if len(r.ConstantResult) != 1 {
		return nil, bad("balanceOf result missing")
	}
	b, err := decodeHex(r.ConstantResult[0], 32)
	if err != nil {
		return nil, err
	}
	return new(big.Int).SetBytes(b), nil
}

// EstimateEnergy: transfer(address,uint256) через triggerconstantcontract.
func (g *TronGrid) EstimateEnergy(ctx context.Context, owner, contract Addr, calldata []byte) (uint64, error) {
	if len(calldata) != transferCallLen {
		return 0, domain.NewError(domain.CodeInternal, ErrDraftCalldata)
	}
	r, err := g.constant(ctx, owner, contract, "transfer(address,uint256)", calldata[4:])
	if err != nil {
		return 0, err
	}
	if r.EnergyUsed == 0 {
		return 0, bad("energy estimate missing")
	}
	return r.EnergyUsed, nil
}

// TRC20Transfers: GET /v1/accounts/{address}/transactions/trc20. Подтверждённые
// и неподтверждённые запрашиваются отдельно, потому что ответ не несёт статуса.
func (g *TronGrid) TRC20Transfers(ctx context.Context, contract, owner Addr, limit int) ([]Transfer, error) {
	if limit <= 0 || limit > 200 {
		limit = 200
	}
	fetch := func(onlyConfirmed bool) ([]Transfer, error) {
		q := url.Values{
			"contract_address": {contract.String()}, "limit": {strconv.Itoa(limit)},
			"only_confirmed": {strconv.FormatBool(onlyConfirmed)}, "order_by": {"block_timestamp,desc"},
		}
		var r struct {
			Data []struct {
				TransactionID  string `json:"transaction_id"`
				BlockTimestamp int64  `json:"block_timestamp"`
				From           string `json:"from"`
				To             string `json:"to"`
				Type           string `json:"type"`
				Value          string `json:"value"`
				TokenInfo      struct {
					Address string `json:"address"`
				} `json:"token_info"`
			} `json:"data"`
		}
		if err := g.get(ctx, "/v1/accounts/"+owner.String()+"/transactions/trc20", pageQuery, &r); err != nil {
			return nil, err
		}
		out := make([]Transfer, 0, len(r.Data))
		for _, d := range r.Data {
			// Адрес токена в записи сверяется с ожидаемым: чужой токен с
			// тем же символом в историю USDT попасть не должен.
			if d.TokenInfo.Address != contract.String() || d.Type != "Transfer" {
				continue
			}
			if _, err := decodeHex(d.TransactionID, 32); err != nil {
				return nil, err
			}
			from, e1 := ParseAddress(d.From)
			to, e2 := ParseAddress(d.To)
			val, e3 := domain.ParseUnits(d.Value)
			if e1 != nil || e2 != nil || e3 != nil || d.BlockTimestamp <= 0 {
				return nil, bad("malformed transfer record")
			}
			out = append(out, Transfer{TxID: strings.ToLower(d.TransactionID), From: from.String(), To: to.String(),
				Value: val, Time: time.UnixMilli(d.BlockTimestamp), Confirmed: onlyConfirmed})
		}
		return out, nil
	}
	conf, err := fetch(true)
	if err != nil {
		return nil, err
	}
	all, err := fetch(false)
	if err != nil {
		return nil, err
	}
	known := make(map[string]bool, len(conf))
	for _, t := range conf {
		known[t.TxID] = true
	}
	out := conf
	for _, t := range all {
		if !known[t.TxID] {
			out = append(out, t) // ещё не подтверждён
		}
	}
	return out, nil
}

// Prices: GET /wallet/getchainparameters.
func (g *TronGrid) Prices(ctx context.Context) (Prices, error) {
	var r struct {
		Params []struct {
			Key   string `json:"key"`
			Value uint64 `json:"value"`
		} `json:"chainParameter"`
	}
	if err := g.get(ctx, "/wallet/getchainparameters", nil, &r); err != nil {
		return Prices{}, err
	}
	var p Prices
	for _, kv := range r.Params {
		switch kv.Key {
		case "getEnergyFee":
			p.EnergyFee = kv.Value
		case "getTransactionFee":
			p.BandwidthFee = kv.Value
		}
	}
	if p.EnergyFee == 0 || p.EnergyFee > maxSaneEnergyFee || p.BandwidthFee == 0 || p.BandwidthFee > maxSaneBandwidthFee {
		return Prices{}, bad("chain prices missing or out of range")
	}
	return p, nil
}

// Broadcast: POST /wallet/broadcasthex. Запрос не помечен идемпотентным:
// повтор после обрыва делает вызывающий после проверки статуса.
func (g *TronGrid) Broadcast(ctx context.Context, tx []byte) (BroadcastResult, error) {
	var r struct {
		Result bool   `json:"result"`
		Code   string `json:"code"`
		TxID   string `json:"txid"`
	}
	err := g.post(ctx, "/wallet/broadcasthex", map[string]string{"transaction": hex.EncodeToString(tx)}, &r, false)
	if err != nil {
		return BroadcastResult{}, err
	}
	res := BroadcastResult{OK: r.Result, Code: sanitizeCode(r.Code), TxID: strings.ToLower(r.TxID)}
	res.Duplicate = !r.Result && r.Code == "DUP_TRANSACTION_ERROR"
	return res, nil
}

// sanitizeCode оставляет в коде отказа только заглавные буквы и подчёркивания.
func sanitizeCode(s string) string {
	if len(s) > 40 {
		return "UNKNOWN"
	}
	for _, c := range []byte(s) {
		if c != '_' && (c < 'A' || c > 'Z') {
			return "UNKNOWN"
		}
	}
	return s
}

// TxInfo: POST /wallet/gettransactioninfobyid и свежий блок для подтверждений.
func (g *TronGrid) TxInfo(ctx context.Context, txid string) (TxInfo, error) {
	if _, err := decodeHex(txid, 32); err != nil {
		return TxInfo{}, err
	}
	var r struct {
		ID          string `json:"id"`
		BlockNumber uint64 `json:"blockNumber"`
		Fee         uint64 `json:"fee"`
		Result      string `json:"result"`
		Receipt     struct {
			Result string `json:"result"`
		} `json:"receipt"`
	}
	if err := g.post(ctx, "/wallet/gettransactioninfobyid", map[string]string{"value": txid}, &r, true); err != nil {
		return TxInfo{}, err
	}
	if r.ID == "" {
		return TxInfo{}, nil // не найдена: ещё не в блоке
	}
	if !strings.EqualFold(r.ID, txid) || r.BlockNumber == 0 {
		return TxInfo{}, bad("transaction info does not match request")
	}
	tip, err := g.NowBlock(ctx)
	if err != nil {
		return TxInfo{}, err
	}
	// Успех: нет поля result == FAILED и receipt.result пуст (простой перевод)
	// или SUCCESS. Всё остальное (REVERT, OUT_OF_ENERGY...) это ошибка.
	ok := r.Result != "FAILED" && (r.Receipt.Result == "" || r.Receipt.Result == "SUCCESS")
	return TxInfo{Found: true, BlockNumber: r.BlockNumber, Success: ok, FeeSun: r.Fee, CurrentTip: tip.Number}, nil
}
