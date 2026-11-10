package money

import (
	"encoding/json"
	"errors"
	"fmt"
	"math/big"
	"regexp"
	"strings"
)

type Currency string

const (
	ILS Currency = "ILS"
	EUR Currency = "EUR"
)

var amountPattern = regexp.MustCompile(`^-?(0|[1-9][0-9]*)\.[0-9]{2}$`)

type Money struct {
	Amount   string   `json:"amount"`
	Currency Currency `json:"currency"`
}

func (m Money) Validate() error {
	if m.Currency != ILS && m.Currency != EUR {
		return fmt.Errorf("unsupported currency %q", m.Currency)
	}
	if !amountPattern.MatchString(m.Amount) || strings.HasPrefix(m.Amount, "-0.00") {
		return errors.New("amount must be an exact two-place decimal string")
	}
	if _, ok := new(big.Rat).SetString(m.Amount); !ok {
		return errors.New("invalid decimal amount")
	}
	return nil
}

func (m *Money) UnmarshalJSON(data []byte) error {
	var raw struct {
		Amount   *string   `json:"amount"`
		Currency *Currency `json:"currency"`
	}
	decoder := json.NewDecoder(strings.NewReader(string(data)))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&raw); err != nil {
		return err
	}
	if raw.Amount == nil || raw.Currency == nil {
		return errors.New("amount and currency are required")
	}
	value := Money{Amount: *raw.Amount, Currency: *raw.Currency}
	if err := value.Validate(); err != nil {
		return err
	}
	*m = value
	return nil
}
