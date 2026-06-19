package pfframe

import (
	"bytes"
	"testing"
)

func TestOpenRoundTrip(t *testing.T) {
	var buf bytes.Buffer
	if err := Encode(&buf, Open, EncodeOpen(7, 5432)); err != nil {
		t.Fatal(err)
	}
	typ, payload, err := Decode(&buf)
	if err != nil || typ != Open {
		t.Fatalf("decode: %v type=%v", err, typ)
	}
	id, port, err := DecodeOpen(payload)
	if err != nil || id != 7 || port != 5432 {
		t.Fatalf("open payload: %d %d %v", id, port, err)
	}
}

func TestDataAndConnIDAndErr(t *testing.T) {
	id, data, err := DecodeData(EncodeData(3, []byte("hello")))
	if err != nil || id != 3 || string(data) != "hello" {
		t.Fatalf("data: %d %q %v", id, data, err)
	}
	cid, err := DecodeConnID(EncodeConnID(9))
	if err != nil || cid != 9 {
		t.Fatalf("connid: %d %v", cid, err)
	}
	eid, msg, err := DecodeConnError(EncodeConnError(4, "refused"))
	if err != nil || eid != 4 || msg != "refused" {
		t.Fatalf("connerr: %d %q %v", eid, msg, err)
	}
}

func TestBadPayloads(t *testing.T) {
	if _, _, err := DecodeOpen([]byte{1, 2, 3}); err == nil {
		t.Fatal("expected error on short open payload")
	}
	if _, _, err := DecodeData([]byte{1, 2}); err == nil {
		t.Fatal("expected error on short data payload")
	}
}
