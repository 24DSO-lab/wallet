package domain_test

import (
	"encoding/json"
	"errors"
	"math/big"
	"strings"
	"testing"

	"github.com/24DSO-lab/wallet/internal/domain"
)

// max256 это 2^256-1 в виде десятичной строки.
const max256 = "115792089237316195423570985008687907853269984665640564039457584007913129639935"

// max256Plus1 это 2^256.
const max256Plus1 = "115792089237316195423570985008687907853269984665640564039457584007913129639936"

func TestParseDecimal(t *testing.T) {
	tests := []struct {
		name     string
		in       string
		decimals uint8
		want     string // минимальные единицы
		wantErr  error
	}{
		// корректный ввод
		{"zero btc", "0", 8, "0", nil},
		{"zero with fraction", "0.0", 8, "0", nil},
		{"one satoshi", "0.00000001", 8, "1", nil},
		{"one btc", "1", 8, "100000000", nil},
		{"btc fraction", "1.5", 8, "150000000", nil},
		{"full precision btc", "21000000.00000001", 8, "2100000000000001", nil},
		{"one wei", "0.000000000000000001", 18, "1", nil},
		{"one eth", "1", 18, "1000000000000000000", nil},
		{"usdc cents", "12.34", 6, "12340000", nil},
		{"usdc min unit", "0.000001", 6, "1", nil},
		{"zero decimals", "42", 0, "42", nil},
		{"trailing zeros inside precision", "1.50", 8, "150000000", nil},
		{"leading zeros allowed", "007.5", 8, "750000000", nil},
		{"max uint256 with 0 decimals", max256, 0, max256, nil},
		{"max uint256 as 18 decimals", "115792089237316195423570985008687907853269984665640564039457.584007913129639935", 18, max256, nil},

		// ошибки
		{"empty", "", 8, "", domain.ErrAmountEmpty},
		{"negative", "-1", 8, "", domain.ErrAmountNegative},
		{"negative zero", "-0", 8, "", domain.ErrAmountNegative},
		{"lone minus", "-", 8, "", domain.ErrAmountNegative},
		{"plus sign", "+1", 8, "", domain.ErrAmountSyntax},
		{"leading space", " 1", 8, "", domain.ErrAmountSyntax},
		{"trailing space", "1 ", 8, "", domain.ErrAmountSyntax},
		{"lone dot", ".", 8, "", domain.ErrAmountSyntax},
		{"no integer part", ".5", 8, "", domain.ErrAmountSyntax},
		{"no fraction after dot", "5.", 8, "", domain.ErrAmountSyntax},
		{"two dots", "1.5.0", 8, "", domain.ErrAmountSyntax},
		{"comma separator", "1,5", 8, "", domain.ErrAmountSyntax},
		{"thousands separator", "1_000", 8, "", domain.ErrAmountSyntax},
		{"exponent", "1e5", 8, "", domain.ErrAmountSyntax},
		{"hex", "0x10", 8, "", domain.ErrAmountSyntax},
		{"letters", "abc", 8, "", domain.ErrAmountSyntax},
		{"arabic-indic digits", "١٢٣", 8, "", domain.ErrAmountSyntax},
		{"fullwidth digits", "１２", 8, "", domain.ErrAmountSyntax},
		{"NaN", "NaN", 8, "", domain.ErrAmountSyntax},
		{"too many decimals btc", "0.123456789", 8, "", domain.ErrAmountPrecision},
		{"extra zeros past precision", "1.000000000", 8, "", domain.ErrAmountPrecision},
		{"fraction with zero decimals", "5.0", 0, "", domain.ErrAmountPrecision},
		{"overflow", max256Plus1, 0, "", domain.ErrAmountOverflow},
		{"overflow by scaling", max256, 1, "", domain.ErrAmountOverflow},
		{"too long input", strings.Repeat("1", 161), 0, "", domain.ErrAmountSyntax},
		{"decimals out of range", "1", 78, "", domain.ErrAmountDecimals},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got, err := domain.ParseDecimal(tc.in, tc.decimals)
			if tc.wantErr != nil {
				if !errors.Is(err, tc.wantErr) {
					t.Fatalf("ParseDecimal(%q, %d) error = %v, want %v", tc.in, tc.decimals, err, tc.wantErr)
				}
				if !got.IsZero() {
					t.Errorf("amount must be zero on error, got %s", got)
				}
				return
			}
			if err != nil {
				t.Fatalf("ParseDecimal(%q, %d) unexpected error: %v", tc.in, tc.decimals, err)
			}
			if got.String() != tc.want {
				t.Errorf("ParseDecimal(%q, %d) = %s, want %s", tc.in, tc.decimals, got, tc.want)
			}
		})
	}
}

func TestParseDecimalMaxDecimals(t *testing.T) {
	// 77 знаков допустимы: 10^77 помещается в 2^256.
	got, err := domain.ParseDecimal("1", 77)
	if err != nil {
		t.Fatalf("decimals=77: %v", err)
	}
	if want := "1" + strings.Repeat("0", 77); got.String() != want {
		t.Errorf("1 with 77 decimals = %s", got)
	}
}

func TestParseUnits(t *testing.T) {
	tests := []struct {
		name    string
		in      string
		want    string
		wantErr error
	}{
		{"zero", "0", "0", nil},
		{"one", "1", "1", nil},
		{"typical", "150000000", "150000000", nil},
		{"max", max256, max256, nil},

		{"empty", "", "", domain.ErrAmountEmpty},
		{"leading zero", "01", "", domain.ErrAmountSyntax},
		{"double zero", "00", "", domain.ErrAmountSyntax},
		{"negative", "-5", "", domain.ErrAmountNegative},
		{"plus", "+5", "", domain.ErrAmountSyntax},
		{"decimal point", "1.5", "", domain.ErrAmountSyntax},
		{"exponent", "1e3", "", domain.ErrAmountSyntax},
		{"space", "1 ", "", domain.ErrAmountSyntax},
		{"unicode digits", "١", "", domain.ErrAmountSyntax},
		{"overflow", max256Plus1, "", domain.ErrAmountOverflow},
		{"too long", strings.Repeat("9", 200), "", domain.ErrAmountSyntax},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got, err := domain.ParseUnits(tc.in)
			if tc.wantErr != nil {
				if !errors.Is(err, tc.wantErr) {
					t.Fatalf("ParseUnits(%q) error = %v, want %v", tc.in, err, tc.wantErr)
				}
				return
			}
			if err != nil {
				t.Fatalf("ParseUnits(%q) unexpected error: %v", tc.in, err)
			}
			if got.String() != tc.want {
				t.Errorf("ParseUnits(%q) = %s, want %s", tc.in, got, tc.want)
			}
		})
	}
}

func TestFormatDecimal(t *testing.T) {
	tests := []struct {
		units    string
		decimals uint8
		want     string
	}{
		{"0", 8, "0"},
		{"1", 8, "0.00000001"},
		{"10", 8, "0.0000001"},
		{"100000000", 8, "1"},
		{"150000000", 8, "1.5"},
		{"2100000000000001", 8, "21000000.00000001"},
		{"1", 18, "0.000000000000000001"},
		{"1000000000000000000", 18, "1"},
		{"1500000000000000000", 18, "1.5"},
		{"12340000", 6, "12.34"},
		{"42", 0, "42"},
		{"5", 1, "0.5"},
		{"10", 1, "1"},
		{max256, 0, max256},
	}
	for _, tc := range tests {
		a, err := domain.ParseUnits(tc.units)
		if err != nil {
			t.Fatalf("ParseUnits(%q): %v", tc.units, err)
		}
		if got := a.FormatDecimal(tc.decimals); got != tc.want {
			t.Errorf("FormatDecimal(%s, %d) = %q, want %q", tc.units, tc.decimals, got, tc.want)
		}
	}
}

func TestDecimalRoundTrip(t *testing.T) {
	for _, decimals := range []uint8{0, 1, 6, 8, 18, 77} {
		for _, units := range []string{"0", "1", "9", "10", "99", "100", "123456789", max256} {
			a, err := domain.ParseUnits(units)
			if err != nil {
				t.Fatal(err)
			}
			text := a.FormatDecimal(decimals)
			back, err := domain.ParseDecimal(text, decimals)
			if err != nil {
				t.Fatalf("ParseDecimal(%q, %d): %v", text, decimals, err)
			}
			if !back.Equal(a) {
				t.Errorf("round trip %s with %d decimals: %q gave %s", units, decimals, text, back)
			}
		}
	}
}

func TestNewAmount(t *testing.T) {
	if _, err := domain.NewAmount(nil); !errors.Is(err, domain.ErrAmountEmpty) {
		t.Errorf("nil: error = %v", err)
	}
	if _, err := domain.NewAmount(big.NewInt(-1)); !errors.Is(err, domain.ErrAmountNegative) {
		t.Errorf("negative: error = %v", err)
	}
	tooBig, _ := new(big.Int).SetString(max256Plus1, 10)
	if _, err := domain.NewAmount(tooBig); !errors.Is(err, domain.ErrAmountOverflow) {
		t.Errorf("2^256: error = %v", err)
	}

	src := big.NewInt(5)
	a, err := domain.NewAmount(src)
	if err != nil {
		t.Fatal(err)
	}
	src.SetInt64(7) // изменение источника не должно влиять на сумму
	if a.String() != "5" {
		t.Errorf("NewAmount must copy its input, got %s", a)
	}
}

func TestBigIntReturnsCopy(t *testing.T) {
	a := domain.AmountFromUint64(100)
	b := a.BigInt()
	b.SetInt64(1)
	if a.String() != "100" {
		t.Errorf("mutating BigInt() result changed the amount: %s", a)
	}
}

func TestZeroValue(t *testing.T) {
	var a domain.Amount
	if !a.IsZero() || a.String() != "0" || a.FormatDecimal(8) != "0" {
		t.Errorf("zero value must be 0, got %q", a)
	}
	b, err := json.Marshal(a)
	if err != nil || string(b) != `"0"` {
		t.Errorf("zero value JSON = %s, %v", b, err)
	}
}

func TestMaxAmount(t *testing.T) {
	if got := domain.MaxAmount().String(); got != max256 {
		t.Errorf("MaxAmount = %s", got)
	}
}

func TestAddSub(t *testing.T) {
	one := domain.AmountFromUint64(1)
	two := domain.AmountFromUint64(2)

	sum, err := one.Add(two)
	if err != nil || sum.String() != "3" {
		t.Errorf("1+2 = %s, %v", sum, err)
	}
	diff, err := two.Sub(one)
	if err != nil || diff.String() != "1" {
		t.Errorf("2-1 = %s, %v", diff, err)
	}
	if _, err := one.Sub(two); !errors.Is(err, domain.ErrAmountNegative) {
		t.Errorf("1-2 error = %v, want ErrAmountNegative", err)
	}
	if _, err := domain.MaxAmount().Add(one); !errors.Is(err, domain.ErrAmountOverflow) {
		t.Errorf("max+1 error = %v, want ErrAmountOverflow", err)
	}
	if z, err := one.Sub(one); err != nil || !z.IsZero() {
		t.Errorf("1-1 = %s, %v", z, err)
	}
}

func TestCmpAndUint64(t *testing.T) {
	one := domain.AmountFromUint64(1)
	two := domain.AmountFromUint64(2)
	if one.Cmp(two) != -1 || two.Cmp(one) != 1 || one.Cmp(one) != 0 || !one.Equal(one) {
		t.Error("Cmp/Equal misbehave")
	}
	if v, ok := two.Uint64(); !ok || v != 2 {
		t.Errorf("Uint64 = %d, %v", v, ok)
	}
	if _, ok := domain.MaxAmount().Uint64(); ok {
		t.Error("Uint64 of 2^256-1 must not fit")
	}
}

func TestAmountJSON(t *testing.T) {
	type payload struct {
		Amount domain.Amount `json:"amount"`
	}

	t.Run("marshal as string", func(t *testing.T) {
		a, _ := domain.ParseUnits("1000000000000000000")
		b, err := json.Marshal(payload{a})
		if err != nil {
			t.Fatal(err)
		}
		if string(b) != `{"amount":"1000000000000000000"}` {
			t.Errorf("got %s", b)
		}
	})

	t.Run("unmarshal string", func(t *testing.T) {
		var p payload
		if err := json.Unmarshal([]byte(`{"amount":"150000000"}`), &p); err != nil {
			t.Fatal(err)
		}
		if p.Amount.String() != "150000000" {
			t.Errorf("got %s", p.Amount)
		}
	})

	t.Run("round trip max", func(t *testing.T) {
		b, _ := json.Marshal(payload{domain.MaxAmount()})
		var p payload
		if err := json.Unmarshal(b, &p); err != nil {
			t.Fatal(err)
		}
		if !p.Amount.Equal(domain.MaxAmount()) {
			t.Errorf("got %s", p.Amount)
		}
	})

	rejects := map[string]string{
		"number":           `{"amount":150000000}`,
		"float":            `{"amount":1.5}`,
		"big number":       `{"amount":100000000000000000000000000}`,
		"null":             `{"amount":null}`,
		"bool":             `{"amount":true}`,
		"object":           `{"amount":{}}`,
		"array":            `{"amount":["1"]}`,
		"empty string":     `{"amount":""}`,
		"decimal string":   `{"amount":"1.5"}`,
		"negative string":  `{"amount":"-1"}`,
		"leading zero":     `{"amount":"01"}`,
		"overflow string":  `{"amount":"` + max256Plus1 + `"}`,
		"exponent string":  `{"amount":"1e3"}`,
		"spaced string":    `{"amount":" 1"}`,
		"unicode escape x": `{"amount":"١"}`,
	}
	for name, doc := range rejects {
		t.Run("reject "+name, func(t *testing.T) {
			p := payload{domain.AmountFromUint64(9)}
			if err := json.Unmarshal([]byte(doc), &p); err == nil {
				t.Fatalf("expected an error for %s, got amount %s", doc, p.Amount)
			}
			if p.Amount.String() != "9" {
				t.Errorf("failed unmarshal must not change the value, got %s", p.Amount)
			}
		})
	}
}

func FuzzParseDecimal(f *testing.F) {
	for _, seed := range []string{"0", "1.5", "0.00000001", "", "-1", ".", "1e5", "٣", max256, max256Plus1, "007.50"} {
		f.Add(seed, uint8(8))
	}
	f.Add("1", uint8(0))
	f.Add("1.000000000000000001", uint8(18))
	f.Fuzz(func(t *testing.T, s string, decimals uint8) {
		a, err := domain.ParseDecimal(s, decimals)
		if err != nil {
			if !a.IsZero() {
				t.Fatalf("non-zero amount %s alongside error %v", a, err)
			}
			return
		}
		if a.Cmp(domain.MaxAmount()) > 0 {
			t.Fatalf("amount %s exceeds the maximum", a)
		}
		// Результат форматируется и разбирается обратно в ту же сумму.
		back, err := domain.ParseDecimal(a.FormatDecimal(decimals), decimals)
		if err != nil || !back.Equal(a) {
			t.Fatalf("round trip of %q (%d decimals) gave %v, %v; want %s", s, decimals, back, err, a)
		}
	})
}

func FuzzParseUnits(f *testing.F) {
	for _, seed := range []string{"0", "1", "01", "-1", "", "1.5", max256, max256Plus1, "٣", "1e3"} {
		f.Add(seed)
	}
	f.Fuzz(func(t *testing.T, s string) {
		a, err := domain.ParseUnits(s)
		if err != nil {
			return
		}
		// Канонический формат: принято ровно то, что выводит String.
		if a.String() != s {
			t.Fatalf("ParseUnits(%q) = %s: accepted input is not canonical", s, a)
		}
	})
}

func FuzzAmountUnmarshalJSON(f *testing.F) {
	for _, seed := range []string{`"1"`, `1`, `null`, `"-1"`, `"` + max256 + `"`, `""`, `"1"`, `{}`, `[`} {
		f.Add([]byte(seed))
	}
	f.Fuzz(func(t *testing.T, data []byte) {
		var a domain.Amount
		if err := a.UnmarshalJSON(data); err != nil {
			if !a.IsZero() {
				t.Fatalf("failed unmarshal left %s", a)
			}
			return
		}
		out, err := a.MarshalJSON()
		if err != nil {
			t.Fatal(err)
		}
		var back domain.Amount
		if err := back.UnmarshalJSON(out); err != nil || !back.Equal(a) {
			t.Fatalf("round trip of %q: %v, %v", data, back, err)
		}
	})
}
