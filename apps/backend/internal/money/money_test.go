package money

import (
	"encoding/json"
	"testing"
)

func TestMoneyValidation(t *testing.T) {
	for _, tc := range []struct {
		json  string
		valid bool
	}{
		{`{"amount":"125.40","currency":"ILS"}`, true},
		{`{"amount":"0.00","currency":"EUR"}`, true},
		{`{"amount":"123456789012345678901234567890.99","currency":"EUR"}`, true},
		{`{"amount":"1.00","currency":"USD"}`, false},
		{`{"amount":125.40,"currency":"ILS"}`, false},
		{`{"amount":"1.2","currency":"ILS"}`, false},
		{`{"amount":"01.00","currency":"ILS"}`, false},
		{`{"amount":"1e2","currency":"ILS"}`, false},
		{`{"amount":"-0.00","currency":"ILS"}`, false},
		{`{"currency":"ILS"}`, false},
	} {
		var value Money
		err := json.Unmarshal([]byte(tc.json), &value)
		if (err == nil) != tc.valid {
			t.Errorf("%s: valid=%v, error=%v", tc.json, tc.valid, err)
		}
	}
}

func TestNullableTax(t *testing.T) {
	var invoice struct {
		Tax *Money `json:"tax"`
	}
	if err := json.Unmarshal([]byte(`{"tax":null}`), &invoice); err != nil || invoice.Tax != nil {
		t.Fatalf("null tax: %#v, %v", invoice.Tax, err)
	}
	if err := json.Unmarshal([]byte(`{"tax":{"amount":"0.00","currency":"ILS"}}`), &invoice); err != nil || invoice.Tax == nil || invoice.Tax.Amount != "0.00" {
		t.Fatalf("zero tax: %#v, %v", invoice.Tax, err)
	}
}
