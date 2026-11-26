/*
 * Copyright 2024 CloudWeGo Authors
 *
 * Licensed under the Apache License, Version 2.0 (the "License");
 * you may not use this file except in compliance with the License.
 * You may obtain a copy of the License at
 *
 *     http://www.apache.org/licenses/LICENSE-2.0
 *
 * Unless required by applicable law or agreed to in writing, software
 * distributed under the License is distributed on an "AS IS" BASIS,
 * WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
 * See the License for the specific language governing permissions and
 * limitations under the License.
 */

package url

import (
	"context"
	"errors"
	"net/http"
	"net/http/cookiejar"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestNewLoaderDefaultClientTimeout(t *testing.T) {
	// This test is deliberately not parallel: temporarily replace the global
	// default client to verify that the loader never mutates it.
	original := http.DefaultClient
	t.Cleanup(func() { http.DefaultClient = original })

	jar, err := cookiejar.New(nil)
	require.NoError(t, err)
	transport := &http.Transport{}
	redirectErr := errors.New("custom redirect policy")
	for _, timeout := range []time.Duration{0, -time.Second, 5 * time.Second} {
		defaultClient := &http.Client{
			Timeout:   timeout,
			Transport: transport,
			Jar:       jar,
			CheckRedirect: func(*http.Request, []*http.Request) error {
				return redirectErr
			},
		}
		http.DefaultClient = defaultClient
		l, err := NewLoader(context.Background(), nil)
		require.NoError(t, err)
		want := timeout
		if want <= 0 {
			want = 30 * time.Second
		}
		require.Equal(t, want, l.conf.Client.Timeout)
		require.NotSame(t, defaultClient, l.conf.Client)
		require.Same(t, transport, l.conf.Client.Transport)
		require.Same(t, jar, l.conf.Client.Jar)
		require.ErrorIs(t, l.conf.Client.CheckRedirect(nil, nil), redirectErr)
		require.Equal(t, timeout, defaultClient.Timeout)
	}
}

func TestNewLoaderKeepsExplicitClient(t *testing.T) {
	for _, timeout := range []time.Duration{0, 5 * time.Second} {
		client := &http.Client{Timeout: timeout}
		l, err := NewLoader(context.Background(), &LoaderConfig{Client: client})
		require.NoError(t, err)
		require.Same(t, client, l.conf.Client)
		require.Equal(t, timeout, client.Timeout)
	}
}
