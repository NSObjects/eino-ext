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

package cache

import (
	"context"
	"fmt"
	"hash"
	"sync"
)

// GeneratorOption holds options for generating unique keys.
type GeneratorOption struct {
	Model string
}

// Generator is an interface for generating unique keys based on text and optional embedding options.
// It is used to create cache keys for embedding results.
type Generator interface {
	Generate(ctx context.Context, text string, opt GeneratorOption) string
}

// SimpleGenerator is a concrete implementation of the Generator interface that generates
// a versioned, length-prefixed key from the text and model without hashing.
type SimpleGenerator struct{}

var _ Generator = (*SimpleGenerator)(nil)

// NewSimpleGenerator creates a new [SimpleGenerator] instance.
func NewSimpleGenerator() *SimpleGenerator {
	return &SimpleGenerator{}
}

func (g *SimpleGenerator) Generate(_ context.Context, text string, opt GeneratorOption) string {
	// A byte length identifies the text/model boundary even for arbitrary strings.
	return fmt.Sprintf("v2:s:%d:%s%s", len(text), text, opt.Model)
}

// HashGenerator is a concrete implementation of the [Generator] interface that uses a hash function
// to generate a unique key based on the provided text and optional embedding options.
// It wraps a [SimpleGenerator] and applies a hash function to the generated key.
//
// The generated key is a version prefix followed by the hex encoded digest of the wrapped
// generator's output. Its length depends only on the prefix and hash function, never
// on the input length. The key no longer directly contains plaintext, but hashing alone does not
// guarantee confidentiality of predictable inputs.
//
// Note: Because of the use of the [hash.Hash] algorithm, there is a probability that data
// with different text and options will generate the same key. This is a trade-off
// between uniqueness and performance. If you need guaranteed uniqueness, consider
// using a different generator or a more complex hashing strategy.
type HashGenerator struct {
	*SimpleGenerator

	// hash.Hash is stateful: Write mutates the running state and Sum reads it. Generate is
	// reachable from every concurrent EmbedStrings call on the same Embedder, so the two
	// operations are serialised here instead of relying on the hasher being thread safe.
	mu     sync.Mutex
	hasher hash.Hash
}

var _ Generator = (*HashGenerator)(nil)

// NewHashGenerator creates a new [HashGenerator] with the specified hash function.
// The generator takes exclusive ownership of hasher; do not share it across generators.
// Use a pointer to HashGenerator and do not copy it after use.
func NewHashGenerator(hasher hash.Hash) *HashGenerator {
	return &HashGenerator{
		SimpleGenerator: NewSimpleGenerator(),
		hasher:          hasher,
	}
}

func (g *HashGenerator) Generate(ctx context.Context, text string, opt GeneratorOption) string {
	plainText := g.SimpleGenerator.Generate(ctx, text, opt)

	g.mu.Lock()
	defer g.mu.Unlock()

	// The hasher is reused across calls, so the previous input has to be dropped first.
	g.hasher.Reset()
	// hash.Hash.Write never returns an error.
	_, _ = g.hasher.Write([]byte(plainText))

	return fmt.Sprintf("v2:h:%x", g.hasher.Sum(nil))
}
