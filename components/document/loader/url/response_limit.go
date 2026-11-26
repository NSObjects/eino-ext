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
	"errors"
	"io"
)

const defaultMaxResponseBytes int64 = 10 << 20

// ErrResponseTooLarge indicates that the response body exceeded
// LoaderConfig.MaxResponseBytes.
var ErrResponseTooLarge = errors.New("response body exceeds the configured limit")

// limitedResponseReader exposes at most limit bytes, then probes one byte to
// distinguish an exact-size body from an oversized one. It retains terminal
// errors so parsers cannot accidentally hide a failed read. It deliberately
// does not own Close: the Loader closes the original response body.
type limitedResponseReader struct {
	reader    io.Reader
	remaining int64
	err       error
}

func newLimitedResponseReader(reader io.Reader, limit int64) *limitedResponseReader {
	if limit <= 0 {
		limit = defaultMaxResponseBytes
	}
	return &limitedResponseReader{reader: reader, remaining: limit}
}

func (r *limitedResponseReader) Read(p []byte) (int, error) {
	if len(p) == 0 {
		return 0, nil
	}
	if r.err != nil {
		return 0, r.err
	}

	// Avoid limit+1 arithmetic, which overflows at MaxInt64.
	probing := r.remaining == 0
	var probe [1]byte
	if probing {
		p = probe[:]
	} else if int64(len(p)) > r.remaining {
		p = p[:r.remaining]
	}
	// Readers may legally return (0, nil) temporarily. Do not mislabel that
	// as overflow, but also do not spin indefinitely on a broken reader.
	for i := 0; i < 100; i++ {
		n, err := r.reader.Read(p)
		if probing && n > 0 {
			r.err = ErrResponseTooLarge
			return 0, r.err
		}
		r.remaining -= int64(n)
		if err != nil {
			r.err = err
		}
		if n > 0 || err != nil {
			return n, err
		}
	}
	r.err = io.ErrNoProgress
	return 0, r.err
}
