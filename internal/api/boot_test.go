package api

import (
	"errors"
	"fmt"
	"testing"
)

func TestIsMethodNotFound(t *testing.T) {
	nf := &RPCError{Code: CodeMethodNotFound, Message: "method not found: x"}
	if !IsMethodNotFound(nf) || !IsMethodNotFound(fmt.Errorf("wrapped: %w", nf)) {
		t.Fatal("method-not-found must match, also when wrapped")
	}
	if IsMethodNotFound(&RPCError{Code: CodeInternalError}) || IsMethodNotFound(errors.New("x")) || IsMethodNotFound(nil) {
		t.Fatal("other errors must not match")
	}
}
