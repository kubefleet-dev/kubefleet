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
package authtoken

import (
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
)

type BufferWriterFactory struct {
	buf        *strings.Builder
	writeCount int
}

func NewBufferWriterFactory() *BufferWriterFactory {
	return &BufferWriterFactory{new(strings.Builder), 0}
}

func (f *BufferWriterFactory) Create() (io.WriteCloser, error) {
	return BufferWriter{f}, nil
}

type BufferWriter struct {
	factory *BufferWriterFactory
}

func (c BufferWriter) Write(p []byte) (int, error) {
	c.factory.writeCount++
	return c.factory.buf.Write(p)
}

func (c BufferWriter) Close() error {
	// no op
	return nil
}

func TestWriteToken(t *testing.T) {
	token := AuthToken{
		Token:     "test token",
		ExpiresOn: time.Now(),
	}

	factory := NewBufferWriterFactory()
	bufferWriter := NewWriter(factory.Create)
	err := bufferWriter.WriteToken(token)

	assert.Equal(t, nil, err, "TestWriteToken")
	assert.Equal(t, token.Token, factory.buf.String(), "TestWriteToken")
}

func TestFactoryCreate(t *testing.T) {
	tests := []struct {
		name     string
		oldToken string
		newToken string
	}{
		{
			name:     "creates new file",
			newToken: "test-token",
		},
		{
			name:     "truncates longer existing content",
			oldToken: "this-is-a-very-long-bearer-token-value",
			newToken: "short-token",
		},
		{
			name:     "overwrites shorter existing content",
			oldToken: "short",
			newToken: "a-much-longer-replacement-token",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			tokenPath := filepath.Join(dir, "token")
			factory := NewFactory(tokenPath)

			writeAndClose := func(token string) {
				wc, err := factory.Create()
				if err != nil {
					t.Fatalf("Factory.Create() returned error: %v", err)
				}
				if _, err := io.WriteString(wc, token); err != nil {
					t.Fatalf("io.WriteString() returned error: %v", err)
				}
				wc.Close()
			}

			if tc.oldToken != "" {
				writeAndClose(tc.oldToken)
			}

			wantToken := tc.newToken
			writeAndClose(wantToken)

			got, err := os.ReadFile(tokenPath)
			if err != nil {
				t.Fatalf("os.ReadFile(%q) returned error: %v", tokenPath, err)
			}
			if string(got) != wantToken {
				t.Errorf("Factory.Create() file content = %q, want %q", string(got), wantToken)
			}

			info, err := os.Stat(tokenPath)
			if err != nil {
				t.Fatalf("os.Stat(%q) returned error: %v", tokenPath, err)
			}
			if gotPerm := info.Mode().Perm(); gotPerm != 0600 {
				t.Errorf("Factory.Create() file permission = %o, want %o", gotPerm, 0600)
			}
		})
	}
}
