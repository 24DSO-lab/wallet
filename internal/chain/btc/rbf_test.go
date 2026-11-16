package btc

import (
	"encoding/json"
	"testing"
)

func TestDecodeSpendDraftRejectsTrailingJSONValue(t *testing.T) {
	encoded, err := json.Marshal(spendDraft{})
	if err != nil {
		t.Fatal(err)
	}
	encoded = append(encoded, []byte("{}")...)

	if _, err := decodeSpendDraft(encoded); err == nil {
		t.Fatal("decodeSpendDraft accepted a second top-level JSON value")
	}
}

func TestDecodeSpendDraftRejectsTrailingGarbage(t *testing.T) {
	encoded, err := json.Marshal(spendDraft{})
	if err != nil {
		t.Fatal(err)
	}
	encoded = append(encoded, []byte(" trailing")...)

	if _, err := decodeSpendDraft(encoded); err == nil {
		t.Fatal("decodeSpendDraft accepted trailing non-JSON data")
	}
}

func TestDecodeSpendDraftAcceptsCanonicalDocument(t *testing.T) {
	encoded, err := json.Marshal(spendDraft{})
	if err != nil {
		t.Fatal(err)
	}

	if _, err := decodeSpendDraft(encoded); err != nil {
		t.Fatalf("decodeSpendDraft rejected canonical document: %v", err)
	}
}
