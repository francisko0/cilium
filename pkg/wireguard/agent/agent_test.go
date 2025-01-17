// SPDX-License-Identifier: Apache-2.0
// Copyright Authors of Cilium

package agent

import (
	"context"
	"iter"
	"maps"
	"net"
	"net/netip"
	"slices"
	"testing"

	"github.com/stretchr/testify/require"
	"golang.org/x/sys/unix"
	"golang.zx2c4.com/wireguard/wgctrl/wgtypes"

	"github.com/cilium/cilium/pkg/cidr"
	iputil "github.com/cilium/cilium/pkg/ip"
	"github.com/cilium/cilium/pkg/ipcache"
	"github.com/cilium/cilium/pkg/source"
	"github.com/cilium/cilium/pkg/wireguard/types"
)

type fakeWgClient struct {
	allowedIPs map[netip.Prefix]wgtypes.Key
	peers      map[wgtypes.Key]wgtypes.Peer
}

func newFakeWgClient(peers ...wgtypes.Peer) *fakeWgClient {
	wgc := &fakeWgClient{
		allowedIPs: make(map[netip.Prefix]wgtypes.Key),
		peers:      make(map[wgtypes.Key]wgtypes.Peer),
	}
	for _, peer := range peers {
		wgc.upsertPeer(peer.PublicKey, peer.Endpoint, peer.AllowedIPs)
	}
	return wgc
}

func (f *fakeWgClient) Close() error {
	return nil
}

func (f *fakeWgClient) Devices() ([]*wgtypes.Device, error) {
	return nil, nil
}

func (f *fakeWgClient) Device(name string) (*wgtypes.Device, error) {
	if name != types.IfaceName {
		return nil, unix.ENODEV
	}

	return &wgtypes.Device{
		Name:  name,
		Peers: slices.Collect(maps.Values(f.peers)),
	}, nil
}

func (f *fakeWgClient) ConfigureDevice(name string, cfg wgtypes.Config) error {
	if name != types.IfaceName {
		return unix.ENODEV
	}

	if cfg.ReplacePeers {
		clear(f.peers)
	}

	for _, peer := range cfg.Peers {
		if peer.Remove {
			for _, ip := range f.peers[peer.PublicKey].AllowedIPs {
				delete(f.allowedIPs, ipnetToPrefix(ip))
			}
			delete(f.peers, peer.PublicKey)
			continue
		}

		if peer.ReplaceAllowedIPs {
			return unix.ENOTSUP
		}

		f.upsertPeer(peer.PublicKey, peer.Endpoint, peer.AllowedIPs)
	}

	return nil
}

func (f *fakeWgClient) upsertPeer(pubKey wgtypes.Key, endpoint *net.UDPAddr, allowedIPs []net.IPNet) {
	for _, ip := range allowedIPs {
		// Steal the IP from another peer potentially.
		if key, exists := f.allowedIPs[ipnetToPrefix(ip)]; exists && key != pubKey {
			oldPeer := f.peers[key]
			oldPeer.AllowedIPs = filterAllowedIPs(oldPeer.AllowedIPs, []net.IPNet{ip})
			f.peers[key] = oldPeer
		}

		f.allowedIPs[ipnetToPrefix(ip)] = pubKey
	}

	f.peers[pubKey] = wgtypes.Peer{
		PublicKey:  pubKey,
		Endpoint:   endpoint,
		AllowedIPs: mergeAllowedIPs(f.peers[pubKey].AllowedIPs, allowedIPs),
	}
}

func mergeAllowedIPs(a, b []net.IPNet) []net.IPNet {
	mergedMap := map[netip.Prefix]net.IPNet{}

	for _, ip := range a {
		mergedMap[ipnetToPrefix(ip)] = ip
	}

	for _, ip := range b {
		mergedMap[ipnetToPrefix(ip)] = ip
	}

	merged := make([]net.IPNet, 0, len(mergedMap))

	for _, ip := range mergedMap {
		merged = append(merged, ip)
	}

	return merged
}

func filterAllowedIPs(ips []net.IPNet, filter []net.IPNet) []net.IPNet {
	filterMap := map[netip.Prefix]net.IPNet{}

	for _, ip := range filter {
		filterMap[ipnetToPrefix(ip)] = ip
	}

	var filtered []net.IPNet
	for _, ip := range ips {
		if _, ok := filterMap[ipnetToPrefix(ip)]; ok {
			continue
		}

		filtered = append(filtered, ip)
	}

	return filtered
}

var (
	k8s1NodeName = "k8s1"
	k8s1PubKey   = "YKQF5gwcQrsZWzxGd4ive+IeCOXjPN4aS9jiMSpAlCg="
	k8s1NodeIPv4 = net.ParseIP("192.168.60.11")
	k8s1NodeIPv6 = net.ParseIP("fd01::b")

	k8s2NodeName   = "k8s2"
	k8s2PubKey     = "lH+Xsa0JClu1syeBVbXN0LZNQVB6rTPBzbzWOHwQLW4="
	k8s2PubKey2    = "UXTzl/X85VYxk03PCtu8JlPbEl+jgqq4M1hkdVp/dCA="
	k8s2NodeIPv4   = net.ParseIP("192.168.60.12")
	k8s2NodeIPv4_2 = net.ParseIP("192.168.60.13")
	k8s2NodeIPv6   = net.ParseIP("fd01::c")
	k8s2NodeIPv6_2 = net.ParseIP("fd01::c")

	pod1IPv4Str = "10.0.0.1"
	pod1IPv4    = iputil.IPToPrefix(net.ParseIP(pod1IPv4Str))
	pod1IPv6Str = "fd00::1"
	pod1IPv6    = iputil.IPToPrefix(net.ParseIP(pod1IPv6Str))
	pod2IPv4Str = "10.0.0.2"
	pod2IPv4    = iputil.IPToPrefix(net.ParseIP(pod2IPv4Str))
	pod2IPv6Str = "fd00::2"
	pod2IPv6    = iputil.IPToPrefix(net.ParseIP(pod2IPv6Str))
	pod3IPv4Str = "10.0.0.3"
	pod3IPv4    = iputil.IPToPrefix(net.ParseIP(pod3IPv4Str))
	pod3IPv6Str = "fd00::3"
	pod3IPv6    = iputil.IPToPrefix(net.ParseIP(pod3IPv6Str))
	pod4IPv4Str = "10.0.0.4"
	pod4IPv4    = iputil.IPToPrefix(net.ParseIP(pod4IPv4Str))
	pod4IPv6Str = "fd00::4"
	pod4IPv6    = iputil.IPToPrefix(net.ParseIP(pod4IPv6Str))
)

func containsIP(allowedIPs iter.Seq[net.IPNet], ipnet *net.IPNet) bool {
	for allowedIP := range allowedIPs {
		if cidr.Equal(&allowedIP, ipnet) {
			return true
		}
	}
	return false
}

func newTestAgent(ctx context.Context, wgClient wireguardClient) (*Agent, *ipcache.IPCache) {
	ipCache := ipcache.NewIPCache(&ipcache.Configuration{
		Context: ctx,
	})
	wgAgent := &Agent{
		wgClient:         wgClient,
		ipCache:          ipCache,
		listenPort:       types.ListenPort,
		peerByNodeName:   map[string]*peerConfig{},
		nodeNameByNodeIP: map[string]string{},
		nodeNameByPubKey: map[wgtypes.Key]string{},
	}
	ipCache.AddListener(wgAgent)
	return wgAgent, ipCache
}

func TestAgent_PeerConfig(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	wgAgent, ipCache := newTestAgent(ctx, newFakeWgClient())
	defer ipCache.Shutdown()

	// Test that IPCache updates before UpdatePeer are handled correctly
	ipCache.Upsert(pod1IPv4Str, k8s1NodeIPv4, 0, nil, ipcache.Identity{ID: 1, Source: source.Kubernetes})
	ipCache.Upsert(pod1IPv6Str, k8s1NodeIPv6, 0, nil, ipcache.Identity{ID: 1, Source: source.Kubernetes})
	ipCache.Upsert(pod2IPv4Str, k8s1NodeIPv4, 0, nil, ipcache.Identity{ID: 2, Source: source.Kubernetes})
	ipCache.Upsert(pod2IPv6Str, k8s1NodeIPv6, 0, nil, ipcache.Identity{ID: 2, Source: source.Kubernetes})

	err := wgAgent.UpdatePeer(k8s1NodeName, k8s1PubKey, k8s1NodeIPv4, k8s1NodeIPv6)
	require.NoError(t, err)

	k8s1 := wgAgent.peerByNodeName[k8s1NodeName]
	require.NotNil(t, k8s1)
	require.EqualValues(t, k8s1NodeIPv4, k8s1.nodeIPv4)
	require.EqualValues(t, k8s1NodeIPv6, k8s1.nodeIPv6)
	require.Equal(t, k8s1PubKey, k8s1.pubKey.String())
	require.Len(t, k8s1.allowedIPs, 6)
	require.True(t, containsIP(maps.Values(k8s1.allowedIPs), iputil.IPToPrefix(k8s1NodeIPv4)))
	require.True(t, containsIP(maps.Values(k8s1.allowedIPs), iputil.IPToPrefix(k8s1NodeIPv6)))
	require.True(t, containsIP(maps.Values(k8s1.allowedIPs), pod1IPv4))
	require.True(t, containsIP(maps.Values(k8s1.allowedIPs), pod1IPv6))
	require.True(t, containsIP(maps.Values(k8s1.allowedIPs), pod2IPv4))
	require.True(t, containsIP(maps.Values(k8s1.allowedIPs), pod2IPv6))

	// Tests that IPCache updates are blocked by a concurrent UpdatePeer.
	// We test this by issuing an UpdatePeer request while holding
	// the agent lock (meaning the UpdatePeer call will first take the IPCache
	// lock and then wait for the agent lock to become available),
	// then issuing an IPCache update (which will be blocked because
	// UpdatePeer already holds the IPCache lock), and then releasing the
	// agent lock to allow both operations to proceed.
	wgAgent.Lock()

	agentUpdated := make(chan struct{})
	agentUpdatePending := make(chan struct{})
	go func() {
		close(agentUpdatePending)
		err = wgAgent.UpdatePeer(k8s2NodeName, k8s2PubKey, k8s2NodeIPv4, k8s2NodeIPv6)
		require.NoError(t, err)
		close(agentUpdated)
	}()

	// wait for the above goroutine to be scheduled
	<-agentUpdatePending

	ipCacheUpdated := make(chan struct{})
	ipCacheUpdatePending := make(chan struct{})
	go func() {
		close(ipCacheUpdatePending)
		// Insert pod3
		ipCache.Upsert(pod3IPv4Str, k8s2NodeIPv4, 0, nil, ipcache.Identity{ID: 3, Source: source.Kubernetes})
		ipCache.Upsert(pod3IPv6Str, k8s2NodeIPv6, 0, nil, ipcache.Identity{ID: 3, Source: source.Kubernetes})
		// Update pod2
		ipCache.Upsert(pod2IPv4Str, k8s1NodeIPv4, 0, nil, ipcache.Identity{ID: 22, Source: source.Kubernetes})
		ipCache.Upsert(pod2IPv6Str, k8s1NodeIPv6, 0, nil, ipcache.Identity{ID: 22, Source: source.Kubernetes})
		// Delete pod1
		ipCache.Delete(pod1IPv4Str, source.Kubernetes)
		ipCache.Delete(pod1IPv6Str, source.Kubernetes)
		close(ipCacheUpdated)
	}()

	// wait for the above goroutine to be scheduled
	<-ipCacheUpdatePending

	// At this point we know both goroutines have been scheduled. We assume
	// that they are now both blocked by checking they haven't closed the
	// channel yet. Thus once release the lock we expect them to make progress
	select {
	case <-agentUpdated:
		t.Fatal("agent update not blocked by agent lock")
	case <-ipCacheUpdated:
		t.Fatal("ipcache update not blocked by agent lock")
	default:
	}

	wgAgent.Unlock()

	// Ensure that both operations succeeded without a deadlock
	<-agentUpdated
	<-ipCacheUpdated

	k8s1 = wgAgent.peerByNodeName[k8s1NodeName]
	require.EqualValues(t, k8s1NodeIPv4, k8s1.nodeIPv4)
	require.EqualValues(t, k8s1NodeIPv6, k8s1.nodeIPv6)
	require.Equal(t, k8s1PubKey, k8s1.pubKey.String())
	require.Len(t, k8s1.allowedIPs, 4)
	require.True(t, containsIP(maps.Values(k8s1.allowedIPs), iputil.IPToPrefix(k8s1NodeIPv4)))
	require.True(t, containsIP(maps.Values(k8s1.allowedIPs), iputil.IPToPrefix(k8s1NodeIPv6)))
	require.True(t, containsIP(maps.Values(k8s1.allowedIPs), pod2IPv4))
	require.True(t, containsIP(maps.Values(k8s1.allowedIPs), pod2IPv6))

	k8s2 := wgAgent.peerByNodeName[k8s2NodeName]
	require.EqualValues(t, k8s2NodeIPv4, k8s2.nodeIPv4)
	require.EqualValues(t, k8s2NodeIPv6, k8s2.nodeIPv6)
	require.Equal(t, k8s2PubKey, k8s2.pubKey.String())
	require.Len(t, k8s2.allowedIPs, 4)
	require.True(t, containsIP(maps.Values(k8s2.allowedIPs), iputil.IPToPrefix(k8s2NodeIPv4)))
	require.True(t, containsIP(maps.Values(k8s2.allowedIPs), iputil.IPToPrefix(k8s2NodeIPv6)))
	require.True(t, containsIP(maps.Values(k8s2.allowedIPs), pod3IPv4))
	require.True(t, containsIP(maps.Values(k8s2.allowedIPs), pod3IPv6))

	// Tests that duplicate public keys are rejected (k8s2 imitates k8s1)
	err = wgAgent.UpdatePeer(k8s2NodeName, k8s1PubKey, k8s2NodeIPv4, k8s2NodeIPv6)
	require.ErrorContains(t, err, "detected duplicate public key")

	// Node Deletion
	wgAgent.DeletePeer(k8s1NodeName)
	wgAgent.DeletePeer(k8s2NodeName)
	require.Empty(t, wgAgent.peerByNodeName)
	require.Empty(t, wgAgent.nodeNameByNodeIP)
	require.Empty(t, wgAgent.nodeNameByPubKey)
}

func TestAgent_PeerConfig_WithEncryptNode(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	wgAgent, ipCache := newTestAgent(ctx, newFakeWgClient())
	defer ipCache.Shutdown()

	ipCache.Upsert(pod1IPv4Str, k8s1NodeIPv4, 0, nil, ipcache.Identity{ID: 1, Source: source.Kubernetes})
	ipCache.Upsert(pod2IPv4Str, k8s1NodeIPv4, 0, nil, ipcache.Identity{ID: 2, Source: source.Kubernetes})

	err := wgAgent.UpdatePeer(k8s1NodeName, k8s1PubKey, k8s1NodeIPv4, k8s1NodeIPv6)
	require.NoError(t, err)

	k8s1 := wgAgent.peerByNodeName[k8s1NodeName]
	require.NotNil(t, k8s1)
	require.EqualValues(t, k8s1NodeIPv4, k8s1.nodeIPv4)
	require.EqualValues(t, k8s1NodeIPv6, k8s1.nodeIPv6)
	require.Equal(t, k8s1PubKey, k8s1.pubKey.String())
	require.Len(t, k8s1.allowedIPs, 4)
	require.True(t, containsIP(maps.Values(k8s1.allowedIPs), pod1IPv4))
	require.True(t, containsIP(maps.Values(k8s1.allowedIPs), pod2IPv4))
	require.True(t, containsIP(maps.Values(k8s1.allowedIPs), iputil.IPToPrefix(k8s1NodeIPv4)))
	require.True(t, containsIP(maps.Values(k8s1.allowedIPs), iputil.IPToPrefix(k8s1NodeIPv6)))
}

func TestAgent_AllowedIPsRestoration(t *testing.T) {
	ctx := context.Background()

	k8s1NodeIPv4Pfx, k8s1NodeIPv6Pfx := iputil.IPToPrefix(k8s1NodeIPv4), iputil.IPToPrefix(k8s1NodeIPv6)
	k8s2NodeIPv4Pfx, k8s2NodeIPv6Pfx := iputil.IPToPrefix(k8s2NodeIPv4), iputil.IPToPrefix(k8s2NodeIPv6)
	k8s2NodeIPv4_2_Pfx, k8s2NodeIPv6_2_Pfx := iputil.IPToPrefix(k8s2NodeIPv4_2), iputil.IPToPrefix(k8s2NodeIPv6_2)

	key1, err := wgtypes.ParseKey(k8s1PubKey)
	require.NoError(t, err, "Failed to parse WG key")
	key2, err := wgtypes.ParseKey(k8s2PubKey)
	require.NoError(t, err, "Failed to parse WG key")
	key2_2, err := wgtypes.ParseKey(k8s2PubKey2)
	require.NoError(t, err, "Failed to parse WG key")

	wgClient := newFakeWgClient(wgtypes.Peer{
		PublicKey: key1,
		AllowedIPs: []net.IPNet{
			*pod1IPv4, *pod3IPv4, *pod2IPv6, *pod3IPv6,
			*pod4IPv4, *pod4IPv6, *k8s1NodeIPv4Pfx, *k8s1NodeIPv6Pfx,
		},
	})

	wgAgent, ipCache := newTestAgent(ctx, wgClient)
	defer ipCache.Shutdown()

	assertAllowedIPs := func(key wgtypes.Key, ips ...*net.IPNet) {
		require.Contains(t, wgClient.peers, key, "The information about the peer should have been upserted")
		allowedIPs := wgClient.peers[key].AllowedIPs
		require.Len(t, allowedIPs, len(ips), "AllowedIPs not updated correctly")
		for _, ip := range ips {
			require.True(t, containsIP(slices.Values(allowedIPs), ip), "AllowedIPs does not contain %s", ip.String())
		}
	}

	ipCache.Upsert(pod1IPv4Str, k8s1NodeIPv4, 0, nil, ipcache.Identity{ID: 1, Source: source.Kubernetes})
	ipCache.Upsert(pod1IPv6Str, k8s1NodeIPv6, 0, nil, ipcache.Identity{ID: 1, Source: source.Kubernetes})

	err = wgAgent.UpdatePeer(k8s1NodeName, k8s1PubKey, k8s1NodeIPv4, k8s1NodeIPv6)
	require.NoError(t, err, "Failed to update peer")

	// Assert that the AllowedIPs are updated correctly, preserving the restored ones
	assertAllowedIPs(key1, pod1IPv4, pod3IPv4, pod4IPv4, pod1IPv6, pod2IPv6, pod3IPv6, pod4IPv6, k8s1NodeIPv4Pfx, k8s1NodeIPv6Pfx)

	// Perform a few ipcache upserts/removal, and assert the AllowedIPs correctness again
	ipCache.Upsert(pod2IPv4Str, k8s1NodeIPv4, 0, nil, ipcache.Identity{ID: 1, Source: source.Kubernetes})
	ipCache.Upsert(pod2IPv6Str, k8s1NodeIPv6, 0, nil, ipcache.Identity{ID: 1, Source: source.Kubernetes})
	ipCache.Delete(pod1IPv4Str, source.Kubernetes)
	assertAllowedIPs(key1, pod2IPv4, pod3IPv4, pod4IPv4, pod1IPv6, pod2IPv6, pod3IPv6, pod4IPv6, k8s1NodeIPv4Pfx, k8s1NodeIPv6Pfx)

	// Associate previously restored allowed IPs with a different peer, and
	// assert that the updates are propagated correctly, without flipping.
	ipCache.Upsert(pod4IPv4Str, k8s2NodeIPv4, 0, nil, ipcache.Identity{ID: 1, Source: source.Kubernetes})
	err = wgAgent.UpdatePeer(k8s2NodeName, k8s2PubKey, k8s2NodeIPv4, k8s2NodeIPv6)
	require.NoError(t, err, "Failed to update peer")
	ipCache.Upsert(pod4IPv6Str, k8s2NodeIPv6, 0, nil, ipcache.Identity{ID: 1, Source: source.Kubernetes})

	// We explicitly trigger UpdatePeer here to cause the allowed IPs to be
	// synchronized, so that we can test that the cache got correctly updated.
	err = wgAgent.UpdatePeer(k8s1NodeName, k8s1PubKey, k8s1NodeIPv4, k8s1NodeIPv6)
	require.NoError(t, err, "Failed to update peer")

	assertAllowedIPs(key1, pod2IPv4, pod3IPv4, pod1IPv6, pod2IPv6, pod3IPv6, k8s1NodeIPv4Pfx, k8s1NodeIPv6Pfx)
	assertAllowedIPs(key2, pod4IPv4, pod4IPv6, k8s2NodeIPv4Pfx, k8s2NodeIPv6Pfx)

	// Run the GC process, and assert the AllowedIPs correctness again
	require.NoError(t, wgAgent.RestoreFinished(nil))
	assertAllowedIPs(key1, pod2IPv4, pod1IPv6, pod2IPv6, k8s1NodeIPv4Pfx, k8s1NodeIPv6Pfx)
	assertAllowedIPs(key2, pod4IPv4, pod4IPv6, k8s2NodeIPv4Pfx, k8s2NodeIPv6Pfx)

	// Ensure that a public key change results in deletion of the old peer entry.
	err = wgAgent.UpdatePeer(k8s2NodeName, k8s2PubKey2, k8s2NodeIPv4, k8s2NodeIPv6)
	require.NoError(t, err)
	assertAllowedIPs(key1, pod2IPv4, pod1IPv6, pod2IPv6, k8s1NodeIPv4Pfx, k8s1NodeIPv6Pfx)
	assertAllowedIPs(key2_2, pod4IPv4, pod4IPv6, k8s2NodeIPv4Pfx, k8s2NodeIPv6Pfx)

	// Ensure that a node IP change gets reflected
	err = wgAgent.UpdatePeer(k8s2NodeName, k8s2PubKey2, k8s2NodeIPv4_2, k8s2NodeIPv6_2)
	require.NoError(t, err)
	assertAllowedIPs(key1, pod2IPv4, pod1IPv6, pod2IPv6, k8s1NodeIPv4Pfx, k8s1NodeIPv6Pfx)
	assertAllowedIPs(key2_2, pod4IPv4, pod4IPv6, k8s2NodeIPv4_2_Pfx, k8s2NodeIPv6_2_Pfx)

	// Ensure that a public key change and node IP change gets reflected.
	err = wgAgent.UpdatePeer(k8s2NodeName, k8s2PubKey, k8s2NodeIPv4, k8s2NodeIPv6)
	require.NoError(t, err)
	assertAllowedIPs(key1, pod2IPv4, pod1IPv6, pod2IPv6, k8s1NodeIPv4Pfx, k8s1NodeIPv6Pfx)
	assertAllowedIPs(key2, pod4IPv4, pod4IPv6, k8s2NodeIPv4Pfx, k8s2NodeIPv6Pfx)

	// Ensure that a node IP change gets reflected
	err = wgAgent.UpdatePeer(k8s2NodeName, wgDummyPeerKey.String(), k8s2NodeIPv4_2, k8s2NodeIPv6_2)
	require.Error(t, err, "node %q is not allowed to use the dummy peer key", k8s2NodeName)
}
