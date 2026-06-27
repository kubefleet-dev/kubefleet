/*
Copyright 2025 The KubeFleet Authors.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

	http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

package controller

import (
	"errors"
	"fmt"
	"sync"
	"testing"

	"github.com/go-logr/logr"
	"k8s.io/klog/v2"
)

// testLogSink is a logr.LogSink that captures error log entries for assertions.
type testLogSink struct {
	mu     sync.Mutex
	logged []loggedEntry
}

type loggedEntry struct {
	err error
	msg string
	kvs []any
}

func (s *testLogSink) Init(logr.RuntimeInfo)          {}
func (s *testLogSink) Enabled(int) bool               { return true }
func (s *testLogSink) Info(int, string, ...any)       {}
func (s *testLogSink) WithValues(...any) logr.LogSink { return s }
func (s *testLogSink) WithName(string) logr.LogSink   { return s }
func (s *testLogSink) Error(err error, msg string, kvs ...any) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.logged = append(s.logged, loggedEntry{err: err, msg: msg, kvs: kvs})
}

// installTestSink replaces the global klog logger with a testLogSink and returns the
// sink together with a cleanup function that restores the previous logger.
func installTestSink() (*testLogSink, func()) {
	sink := &testLogSink{}
	klog.SetLogger(logr.New(sink))
	return sink, func() { klog.ClearLogger() }
}

// TestWrappedErrorIs verifies that errors.Is resolves both the sentinel and the inner
// error from a wrappedError.
func TestWrappedErrorIs(t *testing.T) {
	inner := errors.New("original")
	wrapped := &wrappedError{sentinel: ErrAPIServerError, inner: inner}

	tests := []struct {
		name   string
		target error
		want   bool
	}{
		{
			name:   "matches sentinel",
			target: ErrAPIServerError,
			want:   true,
		},
		{
			name:   "matches inner",
			target: inner,
			want:   true,
		},
		{
			name:   "does not match unrelated sentinel",
			target: ErrUnexpectedBehavior,
			want:   false,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := errors.Is(wrapped, tc.target)
			if got != tc.want {
				t.Errorf("errors.Is(%v, %v) = %v, want %v", wrapped, tc.target, got, tc.want)
			}
		})
	}
}

// TestWrappedErrorIsNested verifies that errors.Is resolves the inner chain when the inner
// error itself wraps another sentinel.
func TestWrappedErrorIsNested(t *testing.T) {
	root := errors.New("root cause")
	// Simulate a chain: outer wraps ErrAPIServerError, inner wraps ErrExpectedBehavior.
	inner := &wrappedError{sentinel: ErrExpectedBehavior, inner: root}
	outer := &wrappedError{sentinel: ErrAPIServerError, inner: inner}

	tests := []struct {
		name   string
		target error
		want   bool
	}{
		{name: "outer sentinel", target: ErrAPIServerError, want: true},
		{name: "inner sentinel", target: ErrExpectedBehavior, want: true},
		{name: "root error", target: root, want: true},
		{name: "unrelated", target: ErrUserError, want: false},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := errors.Is(outer, tc.target)
			if got != tc.want {
				t.Errorf("errors.Is(%v, %v) = %v, want %v", outer, tc.target, got, tc.want)
			}
		})
	}
}

// TestWithValues verifies that WithValues accumulates kv pairs in call order and that
// errors.Is still resolves both the sentinel and the inner error.
func TestWithValues(t *testing.T) {
	inner := errors.New("inner")
	base := &wrappedError{sentinel: ErrAPIServerError, inner: inner, kvs: []any{"k1", "v1"}}

	tests := []struct {
		name       string
		err        error
		kv         []any
		wantKvs    []any
		wantNil    bool
		matchSent  error
		matchInner error
	}{
		{
			name:    "nil input returns nil",
			err:     nil,
			wantNil: true,
		},
		{
			name:       "plain error is wrapped",
			err:        inner,
			kv:         []any{"k2", "v2"},
			wantKvs:    []any{"k2", "v2"},
			matchInner: inner,
		},
		{
			name:       "appends to existing wrappedError",
			err:        base,
			kv:         []any{"k2", "v2"},
			wantKvs:    []any{"k1", "v1", "k2", "v2"},
			matchSent:  ErrAPIServerError,
			matchInner: inner,
		},
		{
			name:       "chained WithValues accumulates in order",
			err:        WithValues(base, "k2", "v2"),
			kv:         []any{"k3", "v3"},
			wantKvs:    []any{"k1", "v1", "k2", "v2", "k3", "v3"},
			matchSent:  ErrAPIServerError,
			matchInner: inner,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := WithValues(tc.err, tc.kv...)
			if tc.wantNil {
				if got != nil {
					t.Errorf("WithValues(%v, ...) = %v, want nil", tc.err, got)
				}
				return
			}

			we, ok := got.(*wrappedError)
			if !ok {
				t.Fatalf("WithValues(%v, ...) = %T, want *wrappedError", tc.err, got)
			}
			// Verify accumulated kvs.
			if len(we.kvs) != len(tc.wantKvs) {
				t.Errorf("WithValues(%v, ...).kvs = %v, want %v", tc.err, we.kvs, tc.wantKvs)
			} else {
				for i := range tc.wantKvs {
					if we.kvs[i] != tc.wantKvs[i] {
						t.Errorf("WithValues(%v, ...).kvs[%d] = %v, want %v", tc.err, i, we.kvs[i], tc.wantKvs[i])
					}
				}
			}
			// Verify errors.Is chains.
			if tc.matchSent != nil && !errors.Is(got, tc.matchSent) {
				t.Errorf("errors.Is(WithValues(%v), %v) = false, want true", tc.err, tc.matchSent)
			}
			if tc.matchInner != nil && !errors.Is(got, tc.matchInner) {
				t.Errorf("errors.Is(WithValues(%v), %v) = false, want true", tc.err, tc.matchInner)
			}
		})
	}
}

// TestLogAndUnwrap verifies that LogAndUnwrap emits exactly one klog.ErrorS call with all
// accumulated kv pairs flattened and returns the inner error.
func TestLogAndUnwrap(t *testing.T) {
	inner := errors.New("inner error")
	plainErr := errors.New("plain error")

	tests := []struct {
		name       string
		err        error
		msg        string
		wantNil    bool
		wantLogN   int   // expected number of log entries emitted
		wantLogErr error // expected error arg in the log entry
		wantLogMsg string
		wantKvKeys []any // just the keys at expected positions, in order
	}{
		{
			name:    "nil input",
			err:     nil,
			msg:     "should not log",
			wantNil: true,
		},
		{
			name:       "plain error logs and returns unchanged",
			err:        plainErr,
			msg:        "plain error occurred",
			wantLogN:   1,
			wantLogErr: plainErr,
			wantLogMsg: "plain error occurred",
		},
		{
			name:       "wrapped error without kvs",
			err:        &wrappedError{sentinel: ErrAPIServerError, inner: inner},
			msg:        "api server call failed",
			wantLogN:   1,
			wantLogErr: inner,
			wantLogMsg: "api server call failed",
		},
		{
			name: "wrapped error with kvs",
			err: &wrappedError{
				sentinel: ErrAPIServerError,
				inner:    inner,
				kvs:      []any{"fromCache", true, "reason", "NotFound"},
			},
			msg:        "api server call failed",
			wantLogN:   1,
			wantLogErr: inner,
			wantLogMsg: "api server call failed",
			wantKvKeys: []any{"fromCache", true, "reason", "NotFound"},
		},
		{
			name: "chained WithValues flattened into single log",
			err: WithValues(
				WithValues(
					&wrappedError{sentinel: ErrAPIServerError, inner: inner, kvs: []any{"k1", "v1"}},
					"k2", "v2",
				),
				"k3", "v3",
			),
			msg:        "chained",
			wantLogN:   1,
			wantLogErr: inner,
			wantLogMsg: "chained",
			wantKvKeys: []any{"k1", "v1", "k2", "v2", "k3", "v3"},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			sink, cleanup := installTestSink()
			defer cleanup()

			got := LogAndUnwrap(tc.err, tc.msg)

			if tc.wantNil {
				if got != nil {
					t.Errorf("LogAndUnwrap(%v, %q) = %v, want nil", tc.err, tc.msg, got)
				}
				if len(sink.logged) != 0 {
					t.Errorf("LogAndUnwrap(nil, ...) emitted %d log entries, want 0", len(sink.logged))
				}
				return
			}

			if len(sink.logged) != tc.wantLogN {
				t.Errorf("LogAndUnwrap(%v, %q) emitted %d log entries, want %d", tc.err, tc.msg, len(sink.logged), tc.wantLogN)
				return
			}

			entry := sink.logged[0]
			if entry.err != tc.wantLogErr {
				t.Errorf("LogAndUnwrap(%v, %q) logged err = %v, want %v", tc.err, tc.msg, entry.err, tc.wantLogErr)
			}
			if entry.msg != tc.wantLogMsg {
				t.Errorf("LogAndUnwrap(%v, %q) logged msg = %q, want %q", tc.err, tc.msg, entry.msg, tc.wantLogMsg)
			}
			if len(tc.wantKvKeys) > 0 {
				if len(entry.kvs) != len(tc.wantKvKeys) {
					t.Errorf("LogAndUnwrap(%v, %q) logged kvs = %v, want %v", tc.err, tc.msg, entry.kvs, tc.wantKvKeys)
				} else {
					for i := range tc.wantKvKeys {
						if entry.kvs[i] != tc.wantKvKeys[i] {
							t.Errorf("LogAndUnwrap(%v, %q) logged kvs[%d] = %v, want %v", tc.err, tc.msg, i, entry.kvs[i], tc.wantKvKeys[i])
						}
					}
				}
			}
		})
	}
}

// TestLogAndUnwrapPlainNoPanic verifies that LogAndUnwrap does not panic when called with
// a plain error (i.e. not a wrappedError).
func TestLogAndUnwrapPlainNoPanic(t *testing.T) {
	_, cleanup := installTestSink()
	defer cleanup()

	plainErr := fmt.Errorf("some error: %w", errors.New("cause"))
	// Should not panic.
	got := LogAndUnwrap(plainErr, "handled")
	if got != plainErr {
		t.Errorf("LogAndUnwrap(%v, ...) = %v, want same plain error", plainErr, got)
	}
}

// TestConstructorsErrorIs verifies that the updated constructors (which now return
// wrappedErrors) still satisfy errors.Is against their respective sentinels, and that
// errors.Is also resolves the inner error.
func TestConstructorsErrorIs(t *testing.T) {
	inner := errors.New("inner")

	tests := []struct {
		name      string
		err       error
		wantSent  error
		wantInner error
	}{
		{
			name:      "NewUnexpectedBehaviorError",
			err:       NewUnexpectedBehaviorError(inner),
			wantSent:  ErrUnexpectedBehavior,
			wantInner: inner,
		},
		{
			name:      "NewExpectedBehaviorError",
			err:       NewExpectedBehaviorError(inner),
			wantSent:  ErrExpectedBehavior,
			wantInner: inner,
		},
		{
			name:      "NewAPIServerError non-cache",
			err:       NewAPIServerError(false, inner),
			wantSent:  ErrAPIServerError,
			wantInner: inner,
		},
		{
			name:      "NewUserError",
			err:       NewUserError(inner),
			wantSent:  ErrUserError,
			wantInner: inner,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if !errors.Is(tc.err, tc.wantSent) {
				t.Errorf("%s = %v, errors.Is(_, %v) = false, want true", tc.name, tc.err, tc.wantSent)
			}
			if !errors.Is(tc.err, tc.wantInner) {
				t.Errorf("%s = %v, errors.Is(_, inner) = false, want true", tc.name, tc.err)
			}
		})
	}
}

// TestConstructorsNoLogSideEffect verifies that the constructors no longer emit klog
// output on their own — logging now happens only at the handler via LogAndUnwrap.
func TestConstructorsNoLogSideEffect(t *testing.T) {
	sink, cleanup := installTestSink()
	defer cleanup()

	inner := errors.New("inner")
	_ = NewUnexpectedBehaviorError(inner)
	_ = NewExpectedBehaviorError(inner)
	_ = NewAPIServerError(false, inner)
	_ = NewUserError(inner)

	if len(sink.logged) != 0 {
		t.Errorf("constructors emitted %d log entries, want 0", len(sink.logged))
	}
}
