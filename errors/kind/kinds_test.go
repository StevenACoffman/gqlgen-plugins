package kind_test

import (
	stderrs "errors"
	"fmt"
	"testing"

	"github.com/Khan/shared-go/errors/kind"
)

func TestIsKind(t *testing.T) {
	errs := map[error]bool{
		fmt.Errorf("not found"):              false,
		stderrs.New(kind.NotAllowed.Error()): false,
		stderrs.New("internal error"):        false,
		kind.GraphqlResponse:                 true,
		kind.Internal:                        true,
		kind.InvalidInput:                    true,
		kind.KhanService:                     true,
		kind.NotAllowed:                      true,
		kind.NotFound:                        true,
		kind.NotImplemented:                  true,
		kind.Service:                         true,
		kind.TransientKhanService:            true,
		kind.TransientService:                true,
		kind.Unauthorized:                    true,
		kind.Unspecified:                     true,
	}
	for err, expected := range errs {
		actual := kind.IsKind(err)
		if actual != expected {
			t.Fatalf(
				"incorrect kind verification! Kind:%+v got: %t wanted:%t",
				err,
				actual,
				expected,
			)
		}
	}
}

func TestAsKind(t *testing.T) {
	errs := map[error]bool{
		fmt.Errorf("not found"):              false,
		stderrs.New(kind.NotAllowed.Error()): false,
		stderrs.New("internal error"):        false,
		kind.GraphqlResponse:                 true,
		kind.Internal:                        true,
		kind.InvalidInput:                    true,
		kind.KhanService:                     true,
		kind.NotAllowed:                      true,
		kind.NotFound:                        true,
		kind.NotImplemented:                  true,
		kind.Service:                         true,
		kind.TransientKhanService:            true,
		kind.TransientService:                true,
		kind.Unauthorized:                    true,
		kind.Unspecified:                     true,
	}

	for err, expected := range errs {
		actual, ok := kind.AsKind(err)
		if ok != expected {
			t.Fatalf(
				"incorrect kind verification! Kind:%+v got: %t wanted:%t",
				err,
				ok,
				expected,
			)
		}
		if expected && err != actual {
			t.Fatalf(
				"incorrect kind verification! Kind:%+v got: %v",
				err,
				actual,
			)
		}
	}
}
