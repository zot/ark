package ark

// CRC: crc-HTTPOperation.md | Seq: seq-http-operation.md

import (
	"encoding/json"
	"errors"
	"net/http"
)

// operation is a request-shaped unit of server work. init takes what the
// request carries and binds the DB access the operation may use; run does
// the work and returns a value to serialize.
//
// run is deliberately HTTP-agnostic — it never touches the
// http.ResponseWriter, never picks a status code, and never writes bytes.
// That is what lets one operation back HTTP, the CLI, and in-process
// callers instead of only the first.
//
// CRC: crc-HTTPOperation.md | R3166
type operation interface {
	init(srv *Server, r *http.Request)
	run() (any, error)
}

// operationPtr constrains PT to be a *T that is an operation, so handle
// can copy the value prototype and still call pointer-receiver methods on
// the copy.
//
// CRC: crc-HTTPOperation.md | R3167
type operationPtr[T any] interface {
	*T
	operation
}

// handle turns an operation prototype into an http.HandlerFunc. Each
// request gets its own copy of the prototype, so a registration-time
// value is safe to share across concurrent requests.
//
// It is a free function taking srv rather than a (*Server).handle method
// because Go does not permit type parameters on methods, and the wrapper
// needs one to name the operation type.
//
// CRC: crc-HTTPOperation.md | Seq: seq-http-operation.md#1.2 | R3167
func handle[T any, PT operationPtr[T]](srv *Server, proto T) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		op := proto // per-request copy — the prototype is never mutated
		p := PT(&op)
		p.init(srv, r)
		result, err := p.run()
		if err != nil {
			http.Error(w, err.Error(), statusFor(err))
			return
		}
		// R3169: a nil result means "succeeded, nothing to report" — no
		// body, and notably no JSON "null". Note this is an untyped nil:
		// a nil *slice* returned as any is not a nil interface, so an
		// operation returning an empty []T still encodes as "null", which
		// is what its pre-operation handler did.
		if result != nil {
			writeJSON(w, result)
		}
	}
}

// opClass is the semantic failure vocabulary an operation reports. It is
// deliberately not an HTTP status: a CLI front door maps these to exit
// codes, and an in-process caller may just switch on them.
//
// CRC: crc-HTTPOperation.md | R3168
type opClass int

const (
	opInternal opClass = iota // unclassified — the safe default
	opBadInput
	opNotFound
	opUnavailable
)

// opError pairs a failure with its class.
//
// CRC: crc-HTTPOperation.md | R3168
type opError struct {
	class opClass
	err   error
}

func (e *opError) Error() string { return e.err.Error() }
func (e *opError) Unwrap() error { return e.err }

// badInput marks a malformed or self-contradictory request.
// CRC: crc-HTTPOperation.md | R3168
func badInput(err error) error { return &opError{opBadInput, err} }

// notFound marks a named thing that does not exist.
// CRC: crc-HTTPOperation.md | R3168
func notFound(err error) error { return &opError{opNotFound, err} }

// unavailable marks a required subsystem that is not configured or running.
// CRC: crc-HTTPOperation.md | R3168
func unavailable(err error) error { return &opError{opUnavailable, err} }

// statusFor maps a classified error to its HTTP status. An unclassified
// error is internal, so the safe default costs no annotation.
//
// CRC: crc-HTTPOperation.md | Seq: seq-http-operation.md#1.9 | R3168
func statusFor(err error) int {
	var oe *opError
	if !errors.As(err, &oe) {
		return http.StatusInternalServerError
	}
	switch oe.class {
	case opBadInput:
		return http.StatusBadRequest
	case opNotFound:
		return http.StatusNotFound
	case opUnavailable:
		return http.StatusServiceUnavailable
	default:
		return http.StatusInternalServerError
	}
}

// decodeBody is the shared request-body decode for operations whose input
// arrives as JSON. A decode failure is bad input, not an internal error.
//
// CRC: crc-HTTPOperation.md | Seq: seq-http-operation.md#1.4 | R3168
func decodeBody(r *http.Request, v any) error {
	if err := json.NewDecoder(r.Body).Decode(v); err != nil {
		return badInput(err)
	}
	return nil
}
