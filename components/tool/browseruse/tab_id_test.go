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
	"testing"

	"github.com/bytedance/mockey"
	"github.com/chromedp/cdproto/target"
	"github.com/chromedp/chromedp"
	"github.com/stretchr/testify/require"
)

func TestSwitchTabUsesAdvertisedID(t *testing.T) {
	b := &Tool{ctx: context.Background()}
	defer mockey.Mock(chromedp.Targets).Return([]*target.Info{
		{Type: "worker", TargetID: "worker"},
		{Type: "page", TargetID: "first"},
		{Type: "service_worker", TargetID: "service"},
		{Type: "page", TargetID: "second"},
	}, nil).Build().UnPatch()
	require.NoError(t, b.updateTabsInfo(b.ctx))
	require.Len(t, b.tabs, 2)
	require.Equal(t, 1, b.tabs[0].ID)
	require.Equal(t, 3, b.tabs[1].ID)

	var activated target.ID
	b.newContext = fakeBrowserContext
	defer mockey.Mock(chromedp.Run).Return(nil).Build().UnPatch()
	defer mockey.Mock((*Tool).updateElements).Return(nil).Build().UnPatch()
	defer mockey.Mock(target.ActivateTarget).To(func(id target.ID) *target.ActivateTargetParams {
		activated = id
		return nil
	}).Build().UnPatch()

	for _, tc := range []struct {
		id   int
		want target.ID
	}{
		{id: 1, want: "first"},
		{id: 3, want: "second"},
	} {
		res, err := b.Execute(&Param{Action: ActionSwitchTab, TabID: &tc.id})
		require.NoError(t, err)
		require.Empty(t, res.Error)
		require.Equal(t, tc.want, activated)
		require.Equal(t, tc.id, b.currentTabID)
	}
	for _, id := range []int{-1, 0, 2, 4} {
		activated = ""
		res, err := b.Execute(&Param{Action: ActionSwitchTab, TabID: &id})
		require.NoError(t, err)
		require.Contains(t, res.Error, "out of range")
		require.Empty(t, activated)
	}
}
