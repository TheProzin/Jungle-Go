package money

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"regexp"
	"strconv"

	"golang.org/x/text/currency"
)

var (
	ErrInvalidAmount    = errors.New("invalid amount")
	ErrInvalidCurrency  = errors.New("invalid currency")
	ErrCurrencyMismatch = errors.New("currency mismatch")
	ErrOverflow         = errors.New("money overflow")
)

var amountPattern = regexp.MustCompile(`^(0|[1-9][0-9]*)\.[0-9]{2}$`)

const maxWhole = math.MaxInt64 / 100

type Money struct {
	minor    int64
	currency string
}

func New(minor int64, currency string) (Money, error) {
	if !validCurrency(currency) {
		return Money{}, fmt.Errorf("%w: %q", ErrInvalidCurrency, currency)
	}
	return Money{minor: minor, currency: currency}, nil
}

func Parse(amount, currency string) (Money, error) {
	if !validCurrency(currency) {
		return Money{}, fmt.Errorf("%w: %q", ErrInvalidCurrency, currency)
	}

	minor, err := parseMinor(amount)
	if err != nil {
		return Money{}, err
	}
	return Money{minor: minor, currency: currency}, nil
}

func Zero(currency string) (Money, error) {
	return New(0, currency)
}

func (m Money) Minor() int64 {
	return m.minor
}

func (m Money) Currency() string {
	return m.currency
}

func (m Money) Amount() string {
	sign := ""
	absolute := uint64(m.minor)
	if m.minor < 0 {
		sign = "-"
		absolute = uint64(-(m.minor + 1)) + 1
	}
	return fmt.Sprintf("%s%d.%02d", sign, absolute/100, absolute%100)
}

func (m Money) IsZero() bool {
	return m.minor == 0
}

func (m Money) IsPositive() bool {
	return m.minor > 0
}

func (m Money) IsNegative() bool {
	return m.minor < 0
}

func (m Money) Add(other Money) (Money, error) {
	if err := ensureCompatible(m, other); err != nil {
		return Money{}, err
	}

	result, ok := addInt64(m.minor, other.minor)
	if !ok {
		return Money{}, ErrOverflow
	}
	return Money{minor: result, currency: m.currency}, nil
}

func (m Money) Sub(other Money) (Money, error) {
	if err := ensureCompatible(m, other); err != nil {
		return Money{}, err
	}

	result, ok := subInt64(m.minor, other.minor)
	if !ok {
		return Money{}, ErrOverflow
	}
	return Money{minor: result, currency: m.currency}, nil
}

func (m Money) Neg() (Money, error) {
	if err := m.validate(); err != nil {
		return Money{}, err
	}
	if m.minor == math.MinInt64 {
		return Money{}, ErrOverflow
	}
	return Money{minor: -m.minor, currency: m.currency}, nil
}

func (m Money) Compare(other Money) (int, error) {
	if err := ensureCompatible(m, other); err != nil {
		return 0, err
	}

	switch {
	case m.minor < other.minor:
		return -1, nil
	case m.minor > other.minor:
		return 1, nil
	default:
		return 0, nil
	}
}

func (m Money) Equal(other Money) bool {
	if m.currency != other.currency || !validCurrency(m.currency) {
		return false
	}
	return m.minor == other.minor
}

func (m Money) MarshalJSON() ([]byte, error) {
	if err := m.validate(); err != nil {
		return nil, err
	}
	return json.Marshal(struct {
		Amount   string `json:"amount"`
		Currency string `json:"currency"`
	}{
		Amount:   m.Amount(),
		Currency: m.currency,
	})
}

func (m *Money) UnmarshalJSON(data []byte) error {
	var payload struct {
		Amount   string `json:"amount"`
		Currency string `json:"currency"`
	}

	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&payload); err != nil {
		return fmt.Errorf("%w: %v", ErrInvalidAmount, err)
	}
	var trailing any
	if err := decoder.Decode(&trailing); !errors.Is(err, io.EOF) {
		return fmt.Errorf("%w: trailing data", ErrInvalidAmount)
	}

	parsed, err := Parse(payload.Amount, payload.Currency)
	if err != nil {
		return err
	}
	*m = parsed
	return nil
}

func parseMinor(amount string) (int64, error) {
	if !amountPattern.MatchString(amount) {
		return 0, fmt.Errorf("%w: %q", ErrInvalidAmount, amount)
	}

	wholeText := amount[:len(amount)-3]
	fractionText := amount[len(amount)-2:]

	whole, err := strconv.ParseInt(wholeText, 10, 64)
	if err != nil {
		return 0, ErrOverflow
	}
	if whole > maxWhole {
		return 0, ErrOverflow
	}

	fraction, err := strconv.ParseInt(fractionText, 10, 64)
	if err != nil {
		return 0, fmt.Errorf("%w: %q", ErrInvalidAmount, amount)
	}

	if whole == maxWhole && fraction > math.MaxInt64-whole*100 {
		return 0, ErrOverflow
	}
	return whole*100 + fraction, nil
}

func validCurrency(code string) bool {
	if len(code) != 3 {
		return false
	}
	unit, err := currency.ParseISO(code)
	if err != nil {
		return false
	}
	return unit != currency.XXX && unit.String() == code
}

func (m Money) validate() error {
	if !validCurrency(m.currency) {
		return fmt.Errorf("%w: %q", ErrInvalidCurrency, m.currency)
	}
	return nil
}

func ensureCompatible(left, right Money) error {
	if err := left.validate(); err != nil {
		return err
	}
	if err := right.validate(); err != nil {
		return err
	}
	if left.currency != right.currency {
		return fmt.Errorf("%w: %s and %s", ErrCurrencyMismatch, left.currency, right.currency)
	}
	return nil
}

func addInt64(left, right int64) (int64, bool) {
	if right > 0 && left > math.MaxInt64-right {
		return 0, false
	}
	if right < 0 && left < math.MinInt64-right {
		return 0, false
	}
	return left + right, true
}

func subInt64(left, right int64) (int64, bool) {
	if right > 0 && left < math.MinInt64+right {
		return 0, false
	}
	if right < 0 && left > math.MaxInt64+right {
		return 0, false
	}
	return left - right, true
}
