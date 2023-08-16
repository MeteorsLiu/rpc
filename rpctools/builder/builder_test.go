package builder

import (
	"testing"
)

func TestBuider(t *testing.T) {
	t.Log(NewAST("testparse_test.go", "ExportStruct", "github.com/xx/xx").Parse().ToGIN())
}
