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

import "k8s.io/klog/v2"

// wrappedError carries a sentinel error, an inner error, and accumulated klog-style
// key-value pairs. It preserves the full error chain so that errors.Is resolves both
// the sentinel and any error nested inside the inner error.
type wrappedError struct {
	// sentinel is the well-known classification error (e.g. ErrAPIServerError).
	// It may be nil when wrappedError is created by WithValues on a plain error.
	sentinel error
	// inner is the original cause.
	inner error
	// kvs are klog-style key-value pairs accumulated from constructors and WithValues calls.
	kvs []any
}

// Error implements the error interface.
func (w *wrappedError) Error() string {
	if w.sentinel != nil {
		return w.sentinel.Error() + ": " + w.inner.Error()
	}
	return w.inner.Error()
}

// Unwrap returns both the sentinel and the inner error so that errors.Is/errors.As
// traverse the full chain. When there is no sentinel only the inner error is returned.
func (w *wrappedError) Unwrap() []error {
	if w.sentinel != nil {
		return []error{w.sentinel, w.inner}
	}
	return []error{w.inner}
}

// WithValues appends klog-style key-value pairs to err and returns the augmented error.
// When err is already a wrappedError the new kvs are appended after any existing ones so
// the call is fully chainable. WithValues returns nil when err is nil.
func WithValues(err error, kv ...any) error {
	if err == nil {
		return nil
	}
	if we, ok := err.(*wrappedError); ok {
		newKvs := make([]any, len(we.kvs)+len(kv))
		copy(newKvs, we.kvs)
		copy(newKvs[len(we.kvs):], kv)
		return &wrappedError{sentinel: we.sentinel, inner: we.inner, kvs: newKvs}
	}
	kvCopy := make([]any, len(kv))
	copy(kvCopy, kv)
	return &wrappedError{inner: err, kvs: kvCopy}
}

// LogAndUnwrap logs err exactly once via klog.ErrorS using msg and any accumulated
// key-value pairs, then returns the unwrapped inner error. For plain (non-wrapped)
// errors it logs the error as-is and returns it unchanged. LogAndUnwrap returns nil
// when err is nil and never panics.
func LogAndUnwrap(err error, msg string) error {
	if err == nil {
		return nil
	}
	if we, ok := err.(*wrappedError); ok {
		klog.ErrorS(we.inner, msg, we.kvs...)
		return we.inner
	}
	klog.ErrorS(err, msg)
	return err
}
