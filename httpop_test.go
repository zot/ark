package ark

// CRC: crc-HTTPOperation.md | Seq: seq-http-operation.md | R3166, R3167, R3168, R3169
// Tests designed in test-HTTPOperation.md.

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
)

// echoOp records the request's "v" query parameter and echoes it back.
// Its whole purpose is to prove the per-request copy: if the prototype
// were shared, concurrent requests would clobber each other's v.
type echoOp struct {
	v string
}

func (o *echoOp) init(srv *Server, r *http.Request) { o.v = r.URL.Query().Get("v") }
func (o *echoOp) run() (any, error)                 { return map[string]string{"v": o.v}, nil }

// TestHandlePrototypeIsCopiedPerRequest — test-HTTPOperation.md
// "prototype is copied per request". Seq: seq-http-operation.md#1.2, R3167.
func TestHandlePrototypeIsCopiedPerRequest(t *testing.T) {
	proto := echoOp{}
	h := handle(nil, proto)

	const n = 32
	var wg sync.WaitGroup
	errs := make(chan string, n)
	for i := range n {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			want := string(rune('a' + i%26))
			rec := httptest.NewRecorder()
			h(rec, httptest.NewRequest("GET", "/?v="+want, nil))
			var got map[string]string
			if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
				errs <- "decode: " + err.Error()
				return
			}
			if got["v"] != want {
				errs <- "got " + got["v"] + ", want " + want
			}
		}(i)
	}
	wg.Wait()
	close(errs)
	for e := range errs {
		t.Error(e)
	}

	// The registration-time value must be untouched by any request.
	if proto.v != "" {
		t.Errorf("prototype was mutated: v = %q, want empty", proto.v)
	}
}

// errOp returns a fixed error (or none) from run, so the wrapper's
// class→status mapping can be exercised directly.
type errOp struct {
	err error
	res any
}

func (o *errOp) init(srv *Server, r *http.Request) {}
func (o *errOp) run() (any, error)                 { return o.res, o.err }

// TestStatusForErrorClasses — test-HTTPOperation.md "semantic error classes
// map to status codes". Seq: seq-http-operation.md#1.9, R3168.
func TestStatusForErrorClasses(t *testing.T) {
	boom := errors.New("boom")
	cases := []struct {
		name string
		err  error
		want int
	}{
		{"bad input", badInput(boom), http.StatusBadRequest},
		{"not found", notFound(boom), http.StatusNotFound},
		{"unavailable", unavailable(boom), http.StatusServiceUnavailable},
		{"unclassified", boom, http.StatusInternalServerError},
		{"success", nil, http.StatusOK},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			rec := httptest.NewRecorder()
			handle(nil, errOp{err: tc.err})(rec, httptest.NewRequest("GET", "/", nil))
			if rec.Code != tc.want {
				t.Errorf("status = %d, want %d", rec.Code, tc.want)
			}
			if tc.err != nil && !strings.Contains(rec.Body.String(), "boom") {
				t.Errorf("error text missing from body: %q", rec.Body.String())
			}
		})
	}
}

// TestNilResultWritesNoBody — test-HTTPOperation.md "nil result writes no
// body". Seq: seq-http-operation.md#1.10, R3169.
func TestNilResultWritesNoBody(t *testing.T) {
	rec := httptest.NewRecorder()
	handle(nil, errOp{})(rec, httptest.NewRequest("GET", "/", nil))

	if rec.Code != http.StatusOK {
		t.Errorf("status = %d, want 200", rec.Code)
	}
	if body, _ := io.ReadAll(rec.Body); len(body) != 0 {
		t.Errorf("body = %q, want empty", body)
	}
	if ct := rec.Header().Get("Content-Type"); ct != "" {
		t.Errorf("Content-Type = %q, want unset", ct)
	}
}

// TestResultIsJSONEncoded — test-HTTPOperation.md "non-nil result is JSON
// encoded". Seq: seq-http-operation.md#1.10, R3169.
func TestResultIsJSONEncoded(t *testing.T) {
	type payload struct {
		Name  string `json:"name"`
		Count int    `json:"count"`
	}
	t.Run("struct", func(t *testing.T) {
		rec := httptest.NewRecorder()
		handle(nil, errOp{res: payload{"ark", 3}})(rec, httptest.NewRequest("GET", "/", nil))
		if ct := rec.Header().Get("Content-Type"); ct != "application/json" {
			t.Errorf("Content-Type = %q, want application/json", ct)
		}
		var got payload
		if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
			t.Fatalf("decode: %v", err)
		}
		if got != (payload{"ark", 3}) {
			t.Errorf("round-trip = %+v, want {ark 3}", got)
		}
	})
	t.Run("slice", func(t *testing.T) {
		rec := httptest.NewRecorder()
		handle(nil, errOp{res: []string{"a", "b"}})(rec, httptest.NewRequest("GET", "/", nil))
		var got []string
		if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
			t.Fatalf("decode: %v", err)
		}
		if len(got) != 2 || got[0] != "a" || got[1] != "b" {
			t.Errorf("round-trip = %v, want [a b]", got)
		}
	})
}

// workOp mirrors the real ops' decode-then-work shape: init stores a decode
// error, run refuses to do anything if one is present. didWork is a pointer
// so the per-request copy still reports back to the test.
type workOp struct {
	decodeErr error
	didWork   *bool
	body      struct {
		Path string `json:"path"`
	}
}

func (o *workOp) init(srv *Server, r *http.Request) { o.decodeErr = decodeBody(r, &o.body) }

func (o *workOp) run() (any, error) {
	if o.decodeErr != nil {
		return nil, o.decodeErr
	}
	*o.didWork = true
	return map[string]string{"path": o.body.Path}, nil
}

// TestInitFailureShortCircuitsRun — test-HTTPOperation.md "init failure
// short-circuits run". Seq: seq-http-operation.md#1.4, R3168.
func TestInitFailureShortCircuitsRun(t *testing.T) {
	t.Run("malformed body", func(t *testing.T) {
		didWork := false
		h := handle(nil, workOp{didWork: &didWork})
		rec := httptest.NewRecorder()
		h(rec, httptest.NewRequest("POST", "/", strings.NewReader("{not json")))

		if rec.Code != http.StatusBadRequest {
			t.Errorf("status = %d, want 400", rec.Code)
		}
		if didWork {
			t.Error("run did work despite a failed decode")
		}
	})
	t.Run("well-formed body", func(t *testing.T) {
		didWork := false
		h := handle(nil, workOp{didWork: &didWork})
		rec := httptest.NewRecorder()
		h(rec, httptest.NewRequest("POST", "/", strings.NewReader(`{"path":"/tmp/x"}`)))

		if rec.Code != http.StatusOK {
			t.Errorf("status = %d, want 200", rec.Code)
		}
		if !didWork {
			t.Error("run did not do work on a valid request")
		}
	})
}
