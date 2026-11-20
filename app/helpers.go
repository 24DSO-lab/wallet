package app

import (
	"errors"
	"net/url"

	"github.com/24DSO-lab/wallet/internal/domain"
)

func transactionExplorerURL(profile domain.Profile, tx domain.Tx) string {
	info, ok := tx.Asset.Info()
	if !ok || tx.TxID == "" {
		return ""
	}
	id := url.PathEscape(string(tx.TxID))
	switch info.Network {
	case domain.NetworkBitcoin:
		if profile == domain.ProfileTest {
			return "https://mempool.space/testnet/tx/" + id
		}
		return "https://mempool.space/tx/" + id
	case domain.NetworkEthereum:
		if profile == domain.ProfileTest {
			return "https://sepolia.etherscan.io/tx/" + id
		}
		return "https://etherscan.io/tx/" + id
	case domain.NetworkTron:
		if profile == domain.ProfileTest {
			return "https://nile.tronscan.org/#/transaction/" + id
		}
		return "https://tronscan.org/#/transaction/" + id
	default:
		return ""
	}
}

func parseReceiveAsset(s string) (domain.Asset, error) {
	if s == string(domain.TRX) {
		return domain.TRX, nil
	}
	return parseAsset(s)
}

func parseAsset(s string) (domain.Asset, error) {
	a, err := domain.ParseAsset(s)
	if err != nil {
		return "", errors.New("invalid_input")
	}
	return a, nil
}

// safeError deliberately exposes only stable error codes, never provider text,
// file paths, SQL details, or secret-bearing causes.
func safeError(err error) error {
	if err == nil {
		return nil
	}
	return errors.New(string(domain.CodeOf(err)))
}

func validatePassword(password string) error {
	if len(password) < 10 {
		return domain.NewError(domain.CodeInvalidInput, errors.New("password must contain at least 10 characters"))
	}
	return nil
}
