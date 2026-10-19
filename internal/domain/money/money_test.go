package money

import (
	"encoding/json"
	"errors"
	"math"
	"testing"
)

func TestParse(t *testing.T) {
	tests := []struct {
		name     string
		amount   string
		currency string
		want     int64
	}{
		{name: "zero", amount: "0.00", currency: "BRL", want: 0},
		{name: "one cent", amount: "0.01", currency: "BRL", want: 1},
		{name: "ten and five cents", amount: "10.05", currency: "BRL", want: 1005},
		{name: "twenty five", amount: "25.00", currency: "BRL", want: 2500},
		{name: "one thousand", amount: "1000.00", currency: "BRL", want: 100000},
		{name: "maximum", amount: "92233720368547758.07", currency: "BRL", want: math.MaxInt64},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			got, err := Parse(test.amount, test.currency)
			if err != nil {
				t.Fatalf("Parse() error = %v", err)
			}
			if got.Minor() != test.want {
				t.Fatalf("Minor() = %d, want %d", got.Minor(), test.want)
			}
			if got.Currency() != test.currency {
				t.Fatalf("Currency() = %q, want %q", got.Currency(), test.currency)
			}
		})
	}
}

func TestParseInvalidAmount(t *testing.T) {
	tests := []string{
		"",
		" ",
		"-0.00",
		"-1.00",
		"+1.00",
		"1",
		"1.",
		".50",
		"1.0",
		"1.000",
		"01.00",
		"00.00",
		"1,00",
		"1e2",
		"NaN",
		"Infinity",
		"abc",
		" 1.00",
		"1.00 ",
		"1.00x",
		"0.0",
	}

	for _, amount := range tests {
		t.Run(amount, func(t *testing.T) {
			_, err := Parse(amount, "BRL")
			if !errors.Is(err, ErrInvalidAmount) {
				t.Fatalf("Parse(%q) error = %v, want ErrInvalidAmount", amount, err)
			}
		})
	}
}

func TestParseInvalidCurrency(t *testing.T) {
	tests := []string{
		"",
		"brl",
		"Brl",
		"BR",
		"BRLL",
		"12A",
		"B R",
		"R$",
		"XYZ",
		"AAA",
		"XXX",
	}

	for _, currency := range tests {
		t.Run(currency, func(t *testing.T) {
			_, err := Parse("1.00", currency)
			if !errors.Is(err, ErrInvalidCurrency) {
				t.Fatalf("Parse(currency=%q) error = %v, want ErrInvalidCurrency", currency, err)
			}
		})
	}
}

func TestParseOverflow(t *testing.T) {
	tests := []string{
		"92233720368547758.08",
		"92233720368547759.00",
		"999999999999999999.99",
		"9223372036854775808.00",
		"999999999999999999999999999999.99",
	}

	for _, amount := range tests {
		t.Run(amount, func(t *testing.T) {
			_, err := Parse(amount, "BRL")
			if !errors.Is(err, ErrOverflow) {
				t.Fatalf("Parse(%q) error = %v, want ErrOverflow", amount, err)
			}
		})
	}
}

func TestNewAllowsNegativeInternalValues(t *testing.T) {
	tests := []struct {
		minor int64
		want  string
	}{
		{minor: -1, want: "-0.01"},
		{minor: -100, want: "-1.00"},
		{minor: math.MinInt64, want: "-92233720368547758.08"},
		{minor: 0, want: "0.00"},
		{minor: 2500, want: "25.00"},
	}

	for _, test := range tests {
		m, err := New(test.minor, "BRL")
		if err != nil {
			t.Fatalf("New(%d) error = %v", test.minor, err)
		}
		if got := m.Amount(); got != test.want {
			t.Fatalf("Amount() = %q, want %q", got, test.want)
		}
	}
}

func TestNewInvalidCurrency(t *testing.T) {
	if _, err := New(100, ""); !errors.Is(err, ErrInvalidCurrency) {
		t.Fatalf("New() error = %v, want ErrInvalidCurrency", err)
	}
}

func TestZero(t *testing.T) {
	zero, err := Zero("BRL")
	if err != nil {
		t.Fatalf("Zero() error = %v", err)
	}
	if !zero.IsZero() {
		t.Fatal("Zero().IsZero() = false, want true")
	}
	if zero.Amount() != "0.00" {
		t.Fatalf("Zero().Amount() = %q, want %q", zero.Amount(), "0.00")
	}

	if _, err := Zero("invalid"); !errors.Is(err, ErrInvalidCurrency) {
		t.Fatalf("Zero(invalid) error = %v, want ErrInvalidCurrency", err)
	}
}

func TestAdd(t *testing.T) {
	one, _ := Parse("1.00", "BRL")
	twoFifty, _ := Parse("2.50", "BRL")
	usd, _ := Parse("1.00", "USD")

	got, err := one.Add(twoFifty)
	if err != nil {
		t.Fatalf("Add() error = %v", err)
	}
	if got.Amount() != "3.50" {
		t.Fatalf("Add() = %q, want %q", got.Amount(), "3.50")
	}

	if _, err := one.Add(usd); !errors.Is(err, ErrCurrencyMismatch) {
		t.Fatalf("Add(USD) error = %v, want ErrCurrencyMismatch", err)
	}

	max, _ := New(math.MaxInt64, "BRL")
	oneMinor, _ := New(1, "BRL")
	if _, err := max.Add(oneMinor); !errors.Is(err, ErrOverflow) {
		t.Fatalf("max.Add(1) error = %v, want ErrOverflow", err)
	}

	min, _ := New(math.MinInt64, "BRL")
	minusOne, _ := New(-1, "BRL")
	if _, err := min.Add(minusOne); !errors.Is(err, ErrOverflow) {
		t.Fatalf("min.Add(-1) error = %v, want ErrOverflow", err)
	}
}

func TestSub(t *testing.T) {
	five, _ := Parse("5.00", "BRL")
	twoFifty, _ := Parse("2.50", "BRL")
	one, _ := Parse("1.00", "BRL")

	got, err := five.Sub(twoFifty)
	if err != nil {
		t.Fatalf("Sub() error = %v", err)
	}
	if got.Amount() != "2.50" {
		t.Fatalf("Sub() = %q, want %q", got.Amount(), "2.50")
	}

	negative, err := one.Sub(twoFifty)
	if err != nil {
		t.Fatalf("Sub() error = %v", err)
	}
	if negative.Amount() != "-1.50" {
		t.Fatalf("Sub() = %q, want %q", negative.Amount(), "-1.50")
	}

	min, _ := New(math.MinInt64, "BRL")
	oneMinor, _ := New(1, "BRL")
	if _, err := min.Sub(oneMinor); !errors.Is(err, ErrOverflow) {
		t.Fatalf("min.Sub(1) error = %v, want ErrOverflow", err)
	}

	max, _ := New(math.MaxInt64, "BRL")
	minusOne, _ := New(-1, "BRL")
	if _, err := max.Sub(minusOne); !errors.Is(err, ErrOverflow) {
		t.Fatalf("max.Sub(-1) error = %v, want ErrOverflow", err)
	}
}

func TestNeg(t *testing.T) {
	one, _ := Parse("1.00", "BRL")

	got, err := one.Neg()
	if err != nil {
		t.Fatalf("Neg() error = %v", err)
	}
	if got.Amount() != "-1.00" {
		t.Fatalf("Neg() = %q, want %q", got.Amount(), "-1.00")
	}

	back, err := got.Neg()
	if err != nil {
		t.Fatalf("Neg() error = %v", err)
	}
	if back.Amount() != "1.00" {
		t.Fatalf("Neg() = %q, want %q", back.Amount(), "1.00")
	}

	min, _ := New(math.MinInt64, "BRL")
	if _, err := min.Neg(); !errors.Is(err, ErrOverflow) {
		t.Fatalf("min.Neg() error = %v, want ErrOverflow", err)
	}
}

func TestCompare(t *testing.T) {
	one, _ := Parse("1.00", "BRL")
	two, _ := Parse("2.00", "BRL")
	otherOne, _ := Parse("1.00", "BRL")
	usd, _ := Parse("1.00", "USD")

	tests := []struct {
		name  string
		left  Money
		right Money
		want  int
	}{
		{name: "less", left: one, right: two, want: -1},
		{name: "greater", left: two, right: one, want: 1},
		{name: "equal", left: one, right: otherOne, want: 0},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			got, err := test.left.Compare(test.right)
			if err != nil {
				t.Fatalf("Compare() error = %v", err)
			}
			if got != test.want {
				t.Fatalf("Compare() = %d, want %d", got, test.want)
			}
		})
	}

	if _, err := one.Compare(usd); !errors.Is(err, ErrCurrencyMismatch) {
		t.Fatalf("Compare(USD) error = %v, want ErrCurrencyMismatch", err)
	}
}

func TestEqual(t *testing.T) {
	one, _ := Parse("1.00", "BRL")
	otherOne, _ := Parse("1.00", "BRL")
	two, _ := Parse("2.00", "BRL")
	usd, _ := Parse("1.00", "USD")

	if !one.Equal(otherOne) {
		t.Fatal("Equal() = false, want true")
	}
	if one.Equal(two) {
		t.Fatal("Equal() = true for different amounts, want false")
	}
	if one.Equal(usd) {
		t.Fatal("Equal() = true for different currencies, want false")
	}
}

func TestMarshalJSON(t *testing.T) {
	value, _ := Parse("25.00", "BRL")

	data, err := json.Marshal(value)
	if err != nil {
		t.Fatalf("MarshalJSON() error = %v", err)
	}

	want := `{"amount":"25.00","currency":"BRL"}`
	if string(data) != want {
		t.Fatalf("MarshalJSON() = %s, want %s", data, want)
	}

	var zero Money
	if _, err := json.Marshal(zero); !errors.Is(err, ErrInvalidCurrency) {
		t.Fatalf("MarshalJSON(zero) error = %v, want ErrInvalidCurrency", err)
	}
}

func TestUnmarshalJSON(t *testing.T) {
	var value Money
	if err := json.Unmarshal([]byte(`{"amount":"25.00","currency":"BRL"}`), &value); err != nil {
		t.Fatalf("UnmarshalJSON() error = %v", err)
	}
	if value.Amount() != "25.00" || value.Currency() != "BRL" {
		t.Fatalf("UnmarshalJSON() = %s %s, want 25.00 BRL", value.Amount(), value.Currency())
	}
}

func TestUnmarshalJSONErrors(t *testing.T) {
	tests := []struct {
		name string
		data string
		want error
	}{
		{name: "invalid amount", data: `{"amount":"-1.00","currency":"BRL"}`, want: ErrInvalidAmount},
		{name: "invalid currency", data: `{"amount":"1.00","currency":"brl"}`, want: ErrInvalidCurrency},
		{name: "missing amount", data: `{"currency":"BRL"}`, want: ErrInvalidAmount},
		{name: "missing currency", data: `{"amount":"1.00"}`, want: ErrInvalidCurrency},
		{name: "numeric amount", data: `{"amount":1.00,"currency":"BRL"}`, want: ErrInvalidAmount},
		{name: "unknown field", data: `{"amount":"1.00","currency":"BRL","extra":true}`, want: ErrInvalidAmount},
		{name: "null", data: `null`, want: ErrInvalidCurrency},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			var value Money
			err := json.Unmarshal([]byte(test.data), &value)
			if !errors.Is(err, test.want) {
				t.Fatalf("UnmarshalJSON(%s) error = %v, want %v", test.data, err, test.want)
			}
		})
	}
}

func TestJSONRoundTrip(t *testing.T) {
	original, _ := New(12345, "BRL")

	data, err := json.Marshal(original)
	if err != nil {
		t.Fatalf("MarshalJSON() error = %v", err)
	}

	var decoded Money
	if err := json.Unmarshal(data, &decoded); err != nil {
		t.Fatalf("UnmarshalJSON() error = %v", err)
	}
	if !original.Equal(decoded) {
		t.Fatalf("round trip = %s, want %s", decoded.Amount(), original.Amount())
	}
}

func TestIsValid(t *testing.T) {
	valid, _ := Parse("1.00", "BRL")
	if !valid.IsValid() {
		t.Fatal("IsValid() = false, want true")
	}

	var zero Money
	if zero.IsValid() {
		t.Fatal("IsValid() = true for zero value, want false")
	}
}

func TestZeroValueRejected(t *testing.T) {
	var zero Money
	one, _ := Parse("1.00", "BRL")

	if _, err := zero.Add(one); !errors.Is(err, ErrInvalidCurrency) {
		t.Fatalf("zero.Add() error = %v, want ErrInvalidCurrency", err)
	}
	if _, err := zero.Neg(); !errors.Is(err, ErrInvalidCurrency) {
		t.Fatalf("zero.Neg() error = %v, want ErrInvalidCurrency", err)
	}
	if zero.Equal(zero) {
		t.Fatal("zero.Equal(zero) = true, want false")
	}
}
