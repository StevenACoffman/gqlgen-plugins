// Package kind implements sentinel errors corresponding to the khan webapp
// errorKind, which are based on gRPC status codes
//
// Sentinel errors are used for flow control.
// Sentinel errors behave like singletons, not constants.
// Each sentinel here is a distinct error value even if the text is identical to some other error.
// Even if you follow the exact procedure used here to create your own myNotFound error value,
// myNotFound and kind.NotFound are not equal. myNotFound and kind.NotFound are not fungible,
// they cannot be interchanged.
//
// If you wrap one of these sentinel errors, and wish to see if which Sentinel
package kind

import (
	stderrs "errors"
)

var (
	// NotFound means that some requested resource wasn't found. If the
	// resource couldn't be retrieved due to access control use
	// Unauthorized instead. If the resource couldn't be found because
	// the input was invalid use InvalidInput instead.
	NotFound = stderrs.New("not found")

	// InvalidInput means that there was a problem with the provided input.
	// This kind indicates inputs that are problematic regardless of the state
	// of the system. Use NotAllowed when the input is valid but
	// conflicts with the state of the system.
	InvalidInput = stderrs.New("invalid input error")

	// NotAllowed means that there was a problem due to the state of
	// the system not matching the requested operation or input. For
	// example, trying to create a username that is valid, but is already
	// taken by another user. Use InvalidInput when the input isn't
	// valid regardless of the state of the system. Use NotFound when
	// the failure is due to not being able to find a resource.
	NotAllowed = stderrs.New("not allowed")

	// Unauthorized means that there was an access control problem.
	Unauthorized = stderrs.New("unauthorized error")

	// Internal means that the function failed for a reason unrelated
	// to its input or problems working with a remote system. Use this kind
	// when other error kinds aren't appropriate.
	Internal = stderrs.New("internal error")

	// NotImplemented means that the function isn't implemented.
	NotImplemented = stderrs.New("not implemented error")

	// GraphqlResponse means that the graphql server returned an
	// error code as part of the graphql response.  This kind of error
	// is only ever returned by gqlclient calls.  It is set when the
	// graphql call successfully executes, but the graphql response struct
	// indicates the graphql request could not be executed due to an
	// error.  (e.g. mutation.MyMutation.Error.Code == "UNAUTHORIZED")
	GraphqlResponse = stderrs.New("graphql error response")

	// TransientKhanService means that there was a problem when contacting
	// another Khan service that might be resolvable by retrying.
	TransientKhanService = stderrs.New("transient khan service error")

	// KhanService means that there was a non-transient problem when
	// contacting another Khan service.
	KhanService = stderrs.New("khan service error")

	// TransientService means that there was a problem when making a
	// request to a non-Khan service, e.g. datastore that might be
	// resolvable by retrying.
	TransientService = stderrs.New("transient service error")

	// Service means that there was a non-transient problem when making a
	// request to a non-Khan service, e.g. datastore.
	Service = stderrs.New("service error")

	// Unspecified means that no error kind was specified. Note that there
	// isn't a constructor for this kind of error.
	Unspecified = stderrs.New("unspecified error")
)

func IsKind(e error) bool {
	switch {
	case stderrs.Is(e, GraphqlResponse),
		stderrs.Is(e, Internal),
		stderrs.Is(e, InvalidInput),
		stderrs.Is(e, KhanService),
		stderrs.Is(e, NotAllowed),
		stderrs.Is(e, NotFound),
		stderrs.Is(e, NotImplemented),
		stderrs.Is(e, Service),
		stderrs.Is(e, TransientKhanService),
		stderrs.Is(e, TransientService),
		stderrs.Is(e, Unauthorized),
		stderrs.Is(e, Unspecified):
		return true
	default:
		return false
	}
}

// AsKind is needed because any sentinel error is *errors.errorString
// so stdlib's errors.As will coerce any one to any other
//
// Also, if an error wraps multiple Kinds, we want the outermost to win
func AsKind(e error) (error, bool) {
	validKinds := []error{
		GraphqlResponse,
		Internal,
		InvalidInput,
		KhanService,
		NotAllowed,
		NotFound,
		NotImplemented,
		Service,
		TransientKhanService,
		TransientService,
		Unauthorized,
		Unspecified,
	}
	for err := e; err != nil; err = unwrapOnce(err) {
		for _, kind := range validKinds {
			if err == kind {
				return kind, true
			}
		}
	}

	return nil, false
}

func unwrapOnce(err error) (cause error) {
	switch e := err.(type) {
	case interface{ Cause() error }:
		return e.Cause()
	case interface{ Unwrap() error }:
		return e.Unwrap()
	}

	return nil
}
