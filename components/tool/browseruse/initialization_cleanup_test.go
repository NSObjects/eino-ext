/*
 * Copyright 2025 CloudWeGo Authors
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

package browseruse

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/bytedance/mockey"
	"github.com/chromedp/chromedp"
	"github.com/stretchr/testify/require"
)

func TestConstructorRollsBackFailedBrowserStart(t *testing.T) {
	parent, cancelParent := context.WithCancel(context.Background())
	defer cancelParent()
	var allocator, child context.Context
	b, err := newBrowserUseTool(parent, &Config{Headless: true}, func(ctx context.Context, _ ...chromedp.ContextOption) (context.Context, context.CancelFunc) {
		allocator = ctx
		// Inherit the allocator-only chromedp context, which Run rejects
		// before attempting to allocate a real browser.
		c, cancel := context.WithCancel(ctx)
		child = c
		return c, cancel
	})
	require.ErrorIs(t, err, chromedp.ErrInvalidContext)
	require.Nil(t, b)
	require.NotNil(t, allocator)
	require.NotNil(t, child)
	require.ErrorIs(t, allocator.Err(), context.Canceled)
	require.ErrorIs(t, child.Err(), context.Canceled)
	require.NoError(t, parent.Err())
}

func TestInitializeRollsBackFailedTabDiscovery(t *testing.T) {
	parent, cancelParent := context.WithCancel(context.Background())
	defer cancelParent()
	wantErr := errors.New("tab discovery failed")
	defer mockey.Mock(chromedp.Run).Return(nil).Build().UnPatch()
	defer mockey.Mock((*Tool).updateTabsInfo).Return(wantErr).Build().UnPatch()

	var allocator, child context.Context
	b := &Tool{
		newContext: func(ctx context.Context, _ ...chromedp.ContextOption) (context.Context, context.CancelFunc) {
			allocator = ctx
			c, cancel := context.WithCancel(ctx)
			child = c
			return c, cancel
		},
	}
	err := b.initialize(parent, &Config{Headless: true})
	require.ErrorIs(t, err, wantErr)
	require.ErrorIs(t, allocator.Err(), context.Canceled)
	require.ErrorIs(t, child.Err(), context.Canceled)
	require.NoError(t, parent.Err())
	require.Nil(t, b.ctx)
	require.Nil(t, b.allocatorCtx)
	require.Nil(t, b.allocatorCancel)
	require.Nil(t, b.tabs)
	require.Nil(t, b.elements)
	b.Cleanup()
}

func TestInitializeDoesNotRelockOnExistingContext(t *testing.T) {
	parent, cancelParent := context.WithCancel(context.Background())
	defer cancelParent()
	oldAllocator, oldCancel := context.WithCancel(parent)
	defer oldCancel()
	oldChild, oldChildCancel := context.WithCancel(oldAllocator)
	defer oldChildCancel()

	b := &Tool{
		ctx:             oldChild,
		allocatorCtx:    oldAllocator,
		allocatorCancel: oldCancel,
		tabs:            []TabInfo{{ID: 3}},
		elements:        []ElementInfo{{Index: 1}},
		currentTabID:    3,
		newContext: func(ctx context.Context, _ ...chromedp.ContextOption) (context.Context, context.CancelFunc) {
			return context.WithCancel(ctx)
		},
	}
	done := make(chan error, 1)
	go func() {
		done <- b.initialize(parent, &Config{Headless: true})
	}()
	select {
	case err := <-done:
		// The injected context deliberately fails Run without starting Chrome.
		require.ErrorIs(t, err, chromedp.ErrInvalidContext)
	case <-time.After(3 * time.Second):
		t.Fatal("initialize blocked while cleaning existing state")
	}
	require.ErrorIs(t, oldAllocator.Err(), context.Canceled)
	require.ErrorIs(t, oldChild.Err(), context.Canceled)
	require.Nil(t, b.ctx)
	require.Nil(t, b.allocatorCtx)
	require.Nil(t, b.allocatorCancel)
	require.Nil(t, b.elements)
	require.Nil(t, b.tabs)
	require.Zero(t, b.currentTabID)
	require.NoError(t, parent.Err())
	b.Cleanup()
}
