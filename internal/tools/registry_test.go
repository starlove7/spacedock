package tools

import (
	"context"
	"encoding/json"
	"testing"
)

func TestRegistryDuplicateBlankSortedCall(t *testing.T) {
	r := NewRegistry()
	mk := func(n string) *BasicTool {
		return &BasicTool{NameValue: n, SchemaValue: json.RawMessage(`{"type":"object"}`), Fn: func(context.Context, json.RawMessage) (Result, error) { return "ok", nil }}
	}
	if e := r.Register(mk("b")); e != nil {
		t.Fatal(e)
	}
	if e := r.Register(mk("a")); e != nil {
		t.Fatal(e)
	}
	if e := r.Register(mk("a")); e == nil {
		t.Fatal("duplicate accepted")
	}
	if e := r.Register(mk("")); e == nil {
		t.Fatal("blank accepted")
	}
	if xs := r.Tools(); xs[0].Name() != "a" {
		t.Fatalf("unsorted")
	}
	v, e := r.Call(context.Background(), "a", nil)
	if e != nil || v != "ok" {
		t.Fatalf("call=%v %v", v, e)
	}
}
func TestDecodeStrictObject(t *testing.T) {
	var x struct {
		A string `json:"a"`
	}
	for _, b := range [][]byte{[]byte("[]"), []byte(`{"a":"x","u":1}`), []byte(`{"a":"x"}{}`)} {
		if e := Decode(b, &x); e == nil {
			t.Fatalf("accepted %s", b)
		}
	}
}
