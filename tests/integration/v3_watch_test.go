// Copyright 2016 The etcd Authors
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//     http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

package integration

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"reflect"
	"sort"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	pb "go.etcd.io/etcd/api/v3/etcdserverpb"
	"go.etcd.io/etcd/api/v3/mvccpb"
	"go.etcd.io/etcd/api/v3/v3rpc/rpctypes"
	clientv3 "go.etcd.io/etcd/client/v3"
	"go.etcd.io/etcd/server/v3/etcdserver/api/v3rpc"
	"go.etcd.io/etcd/server/v3/storage/mvcc"
	"go.etcd.io/etcd/tests/v3/framework/integration"
	gofail "go.etcd.io/gofail/runtime"
)

// TestV3WatchFromCurrentRevision tests Watch APIs from current revision.
func TestV3WatchFromCurrentRevision(t *testing.T) {
	integration.BeforeTest(t)
	tests := []struct {
		name string

		putKeys      []string
		watchRequest *pb.WatchRequest

		wresps []*pb.WatchResponse
	}{
		{
			"watch the key, matching",
			[]string{"foo"},
			&pb.WatchRequest{RequestUnion: &pb.WatchRequest_CreateRequest{
				CreateRequest: &pb.WatchCreateRequest{
					Key: []byte("foo"),
				},
			}},

			[]*pb.WatchResponse{
				{
					Header:  &pb.ResponseHeader{Revision: 2},
					Created: false,
					Events: []*mvccpb.Event{
						{
							Type: mvccpb.PUT,
							Kv:   &mvccpb.KeyValue{Key: []byte("foo"), Value: []byte("bar"), CreateRevision: 2, ModRevision: 2, Version: 1},
						},
					},
				},
			},
		},
		{
			"watch the key, non-matching",
			[]string{"foo"},
			&pb.WatchRequest{RequestUnion: &pb.WatchRequest_CreateRequest{
				CreateRequest: &pb.WatchCreateRequest{
					Key: []byte("helloworld"),
				},
			}},

			[]*pb.WatchResponse{},
		},
		{
			"watch the prefix, matching",
			[]string{"fooLong"},
			&pb.WatchRequest{RequestUnion: &pb.WatchRequest_CreateRequest{
				CreateRequest: &pb.WatchCreateRequest{
					Key:      []byte("foo"),
					RangeEnd: []byte("fop"),
				},
			}},

			[]*pb.WatchResponse{
				{
					Header:  &pb.ResponseHeader{Revision: 2},
					Created: false,
					Events: []*mvccpb.Event{
						{
							Type: mvccpb.PUT,
							Kv:   &mvccpb.KeyValue{Key: []byte("fooLong"), Value: []byte("bar"), CreateRevision: 2, ModRevision: 2, Version: 1},
						},
					},
				},
			},
		},
		{
			"watch the prefix, non-matching",
			[]string{"foo"},
			&pb.WatchRequest{RequestUnion: &pb.WatchRequest_CreateRequest{
				CreateRequest: &pb.WatchCreateRequest{
					Key:      []byte("helloworld"),
					RangeEnd: []byte("helloworle"),
				},
			}},

			[]*pb.WatchResponse{},
		},
		{
			"watch full range, matching",
			[]string{"fooLong"},
			&pb.WatchRequest{RequestUnion: &pb.WatchRequest_CreateRequest{
				CreateRequest: &pb.WatchCreateRequest{
					Key:      []byte(""),
					RangeEnd: []byte("\x00"),
				},
			}},

			[]*pb.WatchResponse{
				{
					Header:  &pb.ResponseHeader{Revision: 2},
					Created: false,
					Events: []*mvccpb.Event{
						{
							Type: mvccpb.PUT,
							Kv:   &mvccpb.KeyValue{Key: []byte("fooLong"), Value: []byte("bar"), CreateRevision: 2, ModRevision: 2, Version: 1},
						},
					},
				},
			},
		},
		{
			"multiple puts, one watcher with matching key",
			[]string{"foo", "foo", "foo"},
			&pb.WatchRequest{RequestUnion: &pb.WatchRequest_CreateRequest{
				CreateRequest: &pb.WatchCreateRequest{
					Key: []byte("foo"),
				},
			}},

			[]*pb.WatchResponse{
				{
					Header:  &pb.ResponseHeader{Revision: 2},
					Created: false,
					Events: []*mvccpb.Event{
						{
							Type: mvccpb.PUT,
							Kv:   &mvccpb.KeyValue{Key: []byte("foo"), Value: []byte("bar"), CreateRevision: 2, ModRevision: 2, Version: 1},
						},
					},
				},
				{
					Header:  &pb.ResponseHeader{Revision: 3},
					Created: false,
					Events: []*mvccpb.Event{
						{
							Type: mvccpb.PUT,
							Kv:   &mvccpb.KeyValue{Key: []byte("foo"), Value: []byte("bar"), CreateRevision: 2, ModRevision: 3, Version: 2},
						},
					},
				},
				{
					Header:  &pb.ResponseHeader{Revision: 4},
					Created: false,
					Events: []*mvccpb.Event{
						{
							Type: mvccpb.PUT,
							Kv:   &mvccpb.KeyValue{Key: []byte("foo"), Value: []byte("bar"), CreateRevision: 2, ModRevision: 4, Version: 3},
						},
					},
				},
			},
		},
		{
			"multiple puts, one watcher with matching prefix",
			[]string{"foo", "foo", "foo"},
			&pb.WatchRequest{RequestUnion: &pb.WatchRequest_CreateRequest{
				CreateRequest: &pb.WatchCreateRequest{
					Key:      []byte("foo"),
					RangeEnd: []byte("fop"),
				},
			}},

			[]*pb.WatchResponse{
				{
					Header:  &pb.ResponseHeader{Revision: 2},
					Created: false,
					Events: []*mvccpb.Event{
						{
							Type: mvccpb.PUT,
							Kv:   &mvccpb.KeyValue{Key: []byte("foo"), Value: []byte("bar"), CreateRevision: 2, ModRevision: 2, Version: 1},
						},
					},
				},
				{
					Header:  &pb.ResponseHeader{Revision: 3},
					Created: false,
					Events: []*mvccpb.Event{
						{
							Type: mvccpb.PUT,
							Kv:   &mvccpb.KeyValue{Key: []byte("foo"), Value: []byte("bar"), CreateRevision: 2, ModRevision: 3, Version: 2},
						},
					},
				},
				{
					Header:  &pb.ResponseHeader{Revision: 4},
					Created: false,
					Events: []*mvccpb.Event{
						{
							Type: mvccpb.PUT,
							Kv:   &mvccpb.KeyValue{Key: []byte("foo"), Value: []byte("bar"), CreateRevision: 2, ModRevision: 4, Version: 3},
						},
					},
				},
			},
		},
	}

	for i, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			clus := integration.NewCluster(t, &integration.ClusterConfig{Size: 3})
			defer clus.Terminate(t)

			wAPI := integration.ToGRPC(clus.RandClient()).Watch
			ctx, cancel := context.WithTimeout(t.Context(), 30*time.Second)
			defer cancel()
			wStream, err := wAPI.Watch(ctx)
			if err != nil {
				t.Fatalf("#%d: wAPI.Watch error: %v", i, err)
			}

			err = wStream.Send(tt.watchRequest)
			if err != nil {
				t.Fatalf("#%d: wStream.Send error: %v", i, err)
			}

			// ensure watcher request created a new watcher
			cresp, err := wStream.Recv()
			if err != nil {
				t.Fatalf("#%d: wStream.Recv error: %v", i, err)
			}
			if !cresp.Created {
				t.Fatalf("#%d: did not create watchid, got %+v", i, cresp)
			}
			if cresp.Canceled {
				t.Fatalf("#%d: canceled watcher on create %+v", i, cresp)
			}

			createdWatchID := cresp.WatchId
			if cresp.Header == nil || cresp.Header.Revision != 1 {
				t.Fatalf("#%d: header revision got +%v, wanted revison 1", i, cresp)
			}

			// asynchronously create keys
			ch := make(chan struct{}, 1)
			go func() {
				for _, k := range tt.putKeys {
					kvc := integration.ToGRPC(clus.RandClient()).KV
					req := &pb.PutRequest{Key: []byte(k), Value: []byte("bar")}
					if _, err := kvc.Put(t.Context(), req); err != nil {
						t.Errorf("#%d: couldn't put key (%v)", i, err)
					}
				}
				ch <- struct{}{}
			}()

			// check stream results
			for j, wresp := range tt.wresps {
				resp, err := wStream.Recv()
				if err != nil {
					t.Errorf("#%d.%d: wStream.Recv error: %v", i, j, err)
				}

				if resp.Header == nil {
					t.Fatalf("#%d.%d: unexpected nil resp.Header", i, j)
				}
				if resp.Header.Revision != wresp.Header.Revision {
					t.Errorf("#%d.%d: resp.Header.Revision got = %d, want = %d", i, j, resp.Header.Revision, wresp.Header.Revision)
				}

				if wresp.Created != resp.Created {
					t.Errorf("#%d.%d: resp.Created got = %v, want = %v", i, j, resp.Created, wresp.Created)
				}
				if resp.WatchId != createdWatchID {
					t.Errorf("#%d.%d: resp.WatchId got = %d, want = %d", i, j, resp.WatchId, createdWatchID)
				}

				if !reflect.DeepEqual(resp.Events, wresp.Events) {
					t.Errorf("#%d.%d: resp.Events got = %+v, want = %+v", i, j, resp.Events, wresp.Events)
				}
			}

			rok, nr := waitResponse(wStream, 1*time.Second)
			if !rok {
				t.Errorf("unexpected pb.WatchResponse is received %+v", nr)
			}

			// wait for the client to finish sending the keys before terminating the cluster
			<-ch
		})
	}
}

// TestV3WatchFutureRevision tests Watch APIs from a future revision.
func TestV3WatchFutureRevision(t *testing.T) {
	integration.BeforeTest(t)

	clus := integration.NewCluster(t, &integration.ClusterConfig{Size: 1})
	defer clus.Terminate(t)

	wAPI := integration.ToGRPC(clus.RandClient()).Watch
	ctx, cancel := context.WithTimeout(t.Context(), 30*time.Second)
	defer cancel()
	wStream, err := wAPI.Watch(ctx)
	if err != nil {
		t.Fatalf("wAPI.Watch error: %v", err)
	}

	wkey := []byte("foo")
	wrev := int64(10)
	req := &pb.WatchRequest{RequestUnion: &pb.WatchRequest_CreateRequest{
		CreateRequest: &pb.WatchCreateRequest{Key: wkey, StartRevision: wrev},
	}}
	err = wStream.Send(req)
	if err != nil {
		t.Fatalf("wStream.Send error: %v", err)
	}

	// ensure watcher request created a new watcher
	cresp, err := wStream.Recv()
	if err != nil {
		t.Fatalf("wStream.Recv error: %v", err)
	}
	if !cresp.Created {
		t.Fatalf("create %v, want %v", cresp.Created, true)
	}

	kvc := integration.ToGRPC(clus.RandClient()).KV
	for {
		req := &pb.PutRequest{Key: wkey, Value: []byte("bar")}
		resp, rerr := kvc.Put(t.Context(), req)
		if rerr != nil {
			t.Fatalf("couldn't put key (%v)", rerr)
		}
		if resp.Header.Revision == wrev {
			break
		}
	}

	// ensure watcher request created a new watcher
	cresp, err = wStream.Recv()
	if err != nil {
		t.Fatalf("wStream.Recv error: %v", err)
	}
	if cresp.Header.Revision != wrev {
		t.Fatalf("revision = %d, want %d", cresp.Header.Revision, wrev)
	}
	if len(cresp.Events) != 1 {
		t.Fatalf("failed to receive events")
	}
	if cresp.Events[0].Kv.ModRevision != wrev {
		t.Errorf("mod revision = %d, want %d", cresp.Events[0].Kv.ModRevision, wrev)
	}
}

// TestV3WatchWrongRange tests wrong range does not create watchers.
func TestV3WatchWrongRange(t *testing.T) {
	integration.BeforeTest(t)

	clus := integration.NewCluster(t, &integration.ClusterConfig{Size: 1})
	defer clus.Terminate(t)

	wAPI := integration.ToGRPC(clus.RandClient()).Watch
	ctx, cancel := context.WithTimeout(t.Context(), 30*time.Second)
	defer cancel()
	wStream, err := wAPI.Watch(ctx)
	if err != nil {
		t.Fatalf("wAPI.Watch error: %v", err)
	}

	tests := []struct {
		key      []byte
		end      []byte
		canceled bool
	}{
		{[]byte("a"), []byte("a"), true},  // wrong range end
		{[]byte("b"), []byte("a"), true},  // wrong range end
		{[]byte("foo"), []byte{0}, false}, // watch request with 'WithFromKey'
	}
	for i, tt := range tests {
		if err := wStream.Send(&pb.WatchRequest{RequestUnion: &pb.WatchRequest_CreateRequest{
			CreateRequest: &pb.WatchCreateRequest{Key: tt.key, RangeEnd: tt.end, StartRevision: 1},
		}}); err != nil {
			t.Fatalf("#%d: wStream.Send error: %v", i, err)
		}
		cresp, err := wStream.Recv()
		if err != nil {
			t.Fatalf("#%d: wStream.Recv error: %v", i, err)
		}
		if !cresp.Created {
			t.Fatalf("#%d: create %v, want %v", i, cresp.Created, true)
		}
		if cresp.Canceled != tt.canceled {
			t.Fatalf("#%d: canceled %v, want %v", i, tt.canceled, cresp.Canceled)
		}
		if tt.canceled && cresp.WatchId != clientv3.InvalidWatchID {
			t.Fatalf("#%d: canceled watch ID %d, want %d", i, cresp.WatchId, clientv3.InvalidWatchID)
		}
	}
}

// TestV3WatchCancelSynced tests Watch APIs cancellation from synced map.
func TestV3WatchCancelSynced(t *testing.T) {
	integration.BeforeTest(t)
	testV3WatchCancel(t, 0)
}

// TestV3WatchCancelUnsynced tests Watch APIs cancellation from unsynced map.
func TestV3WatchCancelUnsynced(t *testing.T) {
	integration.BeforeTest(t)
	testV3WatchCancel(t, 1)
}

func testV3WatchCancel(t *testing.T, startRev int64) {
	clus := integration.NewCluster(t, &integration.ClusterConfig{Size: 3})
	defer clus.Terminate(t)

	ctx, cancel := context.WithTimeout(t.Context(), 30*time.Second)
	defer cancel()
	wStream, errW := integration.ToGRPC(clus.RandClient()).Watch.Watch(ctx)
	if errW != nil {
		t.Fatalf("wAPI.Watch error: %v", errW)
	}

	wreq := &pb.WatchRequest{RequestUnion: &pb.WatchRequest_CreateRequest{
		CreateRequest: &pb.WatchCreateRequest{
			Key: []byte("foo"), StartRevision: startRev,
		},
	}}
	if err := wStream.Send(wreq); err != nil {
		t.Fatalf("wStream.Send error: %v", err)
	}

	wresp, errR := wStream.Recv()
	if errR != nil {
		t.Errorf("wStream.Recv error: %v", errR)
	}
	if !wresp.Created {
		t.Errorf("wresp.Created got = %v, want = true", wresp.Created)
	}

	creq := &pb.WatchRequest{RequestUnion: &pb.WatchRequest_CancelRequest{
		CancelRequest: &pb.WatchCancelRequest{
			WatchId: wresp.WatchId,
		},
	}}
	if err := wStream.Send(creq); err != nil {
		t.Fatalf("wStream.Send error: %v", err)
	}

	cresp, err := wStream.Recv()
	if err != nil {
		t.Errorf("wStream.Recv error: %v", err)
	}
	if !cresp.Canceled {
		t.Errorf("cresp.Canceled got = %v, want = true", cresp.Canceled)
	}

	kvc := integration.ToGRPC(clus.RandClient()).KV
	if _, err := kvc.Put(t.Context(), &pb.PutRequest{Key: []byte("foo"), Value: []byte("bar")}); err != nil {
		t.Errorf("couldn't put key (%v)", err)
	}

	// watch got canceled, so this should block
	rok, nr := waitResponse(wStream, 1*time.Second)
	if !rok {
		t.Errorf("unexpected pb.WatchResponse is received %+v", nr)
	}
}

// TestV3WatchCurrentPutOverlap ensures current watchers receive all events with
// overlapping puts.
func TestV3WatchCurrentPutOverlap(t *testing.T) {
	integration.BeforeTest(t)
	clus := integration.NewCluster(t, &integration.ClusterConfig{Size: 3})
	defer clus.Terminate(t)

	ctx, cancel := context.WithTimeout(t.Context(), 30*time.Second)
	defer cancel()
	wStream, wErr := integration.ToGRPC(clus.RandClient()).Watch.Watch(ctx)
	if wErr != nil {
		t.Fatalf("wAPI.Watch error: %v", wErr)
	}

	// last mod_revision that will be observed
	nrRevisions := 32
	// first revision already allocated as empty revision
	var wg sync.WaitGroup
	for i := 1; i < nrRevisions; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			kvc := integration.ToGRPC(clus.RandClient()).KV
			req := &pb.PutRequest{Key: []byte("foo"), Value: []byte("bar")}
			if _, err := kvc.Put(t.Context(), req); err != nil {
				t.Errorf("couldn't put key (%v)", err)
			}
		}()
	}

	// maps watcher to current expected revision
	progress := make(map[int64]int64)

	wreq := &pb.WatchRequest{RequestUnion: &pb.WatchRequest_CreateRequest{
		CreateRequest: &pb.WatchCreateRequest{Key: []byte("foo"), RangeEnd: []byte("fop")},
	}}
	if err := wStream.Send(wreq); err != nil {
		t.Fatalf("first watch request failed (%v)", err)
	}

	more := true
	progress[-1] = 0 // watcher creation pending
	for more {
		resp, err := wStream.Recv()
		if err != nil {
			t.Fatalf("wStream.Recv error: %v", err)
		}

		if resp.Created {
			// accept events > header revision
			progress[resp.WatchId] = resp.Header.Revision + 1
			if resp.Header.Revision == int64(nrRevisions) {
				// covered all revisions; create no more watchers
				progress[-1] = int64(nrRevisions) + 1
			} else if err := wStream.Send(wreq); err != nil {
				t.Fatalf("watch request failed (%v)", err)
			}
		} else if len(resp.Events) == 0 {
			t.Fatalf("got events %v, want non-empty", resp.Events)
		} else {
			wRev, ok := progress[resp.WatchId]
			if !ok {
				t.Fatalf("got %+v, but watch id shouldn't exist ", resp)
			}
			if resp.Events[0].Kv.ModRevision != wRev {
				t.Fatalf("got %+v, wanted first revision %d", resp, wRev)
			}
			lastRev := resp.Events[len(resp.Events)-1].Kv.ModRevision
			progress[resp.WatchId] = lastRev + 1
		}
		more = false
		for _, v := range progress {
			if v <= int64(nrRevisions) {
				more = true
				break
			}
		}
	}

	if rok, nr := waitResponse(wStream, time.Second); !rok {
		t.Errorf("unexpected pb.WatchResponse is received %+v", nr)
	}

	wg.Wait()
}

// TestV3WatchEmptyKey ensures synced watchers see empty key PUTs as PUT events
func TestV3WatchEmptyKey(t *testing.T) {
	integration.BeforeTest(t)

	clus := integration.NewCluster(t, &integration.ClusterConfig{Size: 1})
	defer clus.Terminate(t)

	ctx, cancel := context.WithTimeout(t.Context(), 30*time.Second)
	defer cancel()

	ws, werr := integration.ToGRPC(clus.RandClient()).Watch.Watch(ctx)
	require.NoError(t, werr)
	req := &pb.WatchRequest{RequestUnion: &pb.WatchRequest_CreateRequest{
		CreateRequest: &pb.WatchCreateRequest{
			Key: []byte("foo"),
		},
	}}
	require.NoError(t, ws.Send(req))
	_, err := ws.Recv()
	require.NoError(t, err)

	// put a key with empty value
	kvc := integration.ToGRPC(clus.RandClient()).KV
	preq := &pb.PutRequest{Key: []byte("foo")}
	_, err = kvc.Put(t.Context(), preq)
	require.NoError(t, err)

	// check received PUT
	resp, rerr := ws.Recv()
	require.NoError(t, rerr)
	wevs := []*mvccpb.Event{
		{
			Type: mvccpb.PUT,
			Kv:   &mvccpb.KeyValue{Key: []byte("foo"), CreateRevision: 2, ModRevision: 2, Version: 1},
		},
	}
	if !reflect.DeepEqual(resp.Events, wevs) {
		t.Fatalf("got %v, expected %v", resp.Events, wevs)
	}
}

func TestV3WatchMultipleWatchersSynced(t *testing.T) {
	integration.BeforeTest(t)
	testV3WatchMultipleWatchers(t, 0)
}

func TestV3WatchMultipleWatchersUnsynced(t *testing.T) {
	integration.BeforeTest(t)
	testV3WatchMultipleWatchers(t, 1)
}

// testV3WatchMultipleWatchers tests multiple watchers on the same key
// and one watcher with matching prefix. It first puts the key
// that matches all watchers, and another key that matches only
// one watcher to test if it receives expected events.
func testV3WatchMultipleWatchers(t *testing.T, startRev int64) {
	clus := integration.NewCluster(t, &integration.ClusterConfig{Size: 3})
	defer clus.Terminate(t)

	kvc := integration.ToGRPC(clus.RandClient()).KV

	ctx, cancel := context.WithTimeout(t.Context(), 30*time.Second)
	defer cancel()
	wStream, errW := integration.ToGRPC(clus.RandClient()).Watch.Watch(ctx)
	if errW != nil {
		t.Fatalf("wAPI.Watch error: %v", errW)
	}

	watchKeyN := 4
	for i := 0; i < watchKeyN+1; i++ {
		var wreq *pb.WatchRequest
		if i < watchKeyN {
			wreq = &pb.WatchRequest{RequestUnion: &pb.WatchRequest_CreateRequest{
				CreateRequest: &pb.WatchCreateRequest{
					Key: []byte("foo"), StartRevision: startRev,
				},
			}}
		} else {
			wreq = &pb.WatchRequest{RequestUnion: &pb.WatchRequest_CreateRequest{
				CreateRequest: &pb.WatchCreateRequest{
					Key: []byte("fo"), RangeEnd: []byte("fp"), StartRevision: startRev,
				},
			}}
		}
		if err := wStream.Send(wreq); err != nil {
			t.Fatalf("wStream.Send error: %v", err)
		}
	}

	ids := make(map[int64]struct{})
	for i := 0; i < watchKeyN+1; i++ {
		wresp, err := wStream.Recv()
		if err != nil {
			t.Fatalf("wStream.Recv error: %v", err)
		}
		if !wresp.Created {
			t.Fatalf("wresp.Created got = %v, want = true", wresp.Created)
		}
		ids[wresp.WatchId] = struct{}{}
	}

	if _, err := kvc.Put(t.Context(), &pb.PutRequest{Key: []byte("foo"), Value: []byte("bar")}); err != nil {
		t.Fatalf("couldn't put key (%v)", err)
	}

	for i := 0; i < watchKeyN+1; i++ {
		wresp, err := wStream.Recv()
		if err != nil {
			t.Fatalf("wStream.Recv error: %v", err)
		}
		if _, ok := ids[wresp.WatchId]; !ok {
			t.Errorf("watchId %d is not created!", wresp.WatchId)
		} else {
			delete(ids, wresp.WatchId)
		}
		if len(wresp.Events) == 0 {
			t.Errorf("#%d: no events received", i)
		}
		for _, ev := range wresp.Events {
			if string(ev.Kv.Key) != "foo" {
				t.Errorf("ev.Kv.Key got = %s, want = foo", ev.Kv.Key)
			}
			if string(ev.Kv.Value) != "bar" {
				t.Errorf("ev.Kv.Value got = %s, want = bar", ev.Kv.Value)
			}
		}
	}

	// now put one key that has only one matching watcher
	if _, err := kvc.Put(t.Context(), &pb.PutRequest{Key: []byte("fo"), Value: []byte("bar")}); err != nil {
		t.Fatalf("couldn't put key (%v)", err)
	}
	wresp, err := wStream.Recv()
	if err != nil {
		t.Errorf("wStream.Recv error: %v", err)
	}
	if len(wresp.Events) != 1 {
		t.Fatalf("len(wresp.Events) got = %d, want = 1", len(wresp.Events))
	}
	if string(wresp.Events[0].Kv.Key) != "fo" {
		t.Errorf("wresp.Events[0].Kv.Key got = %s, want = fo", wresp.Events[0].Kv.Key)
	}

	// now Recv should block because there is no more events coming
	rok, nr := waitResponse(wStream, 1*time.Second)
	if !rok {
		t.Errorf("unexpected pb.WatchResponse is received %+v", nr)
	}
}

func TestV3WatchMultipleEventsTxnSynced(t *testing.T) {
	integration.BeforeTest(t)
	testV3WatchMultipleEventsTxn(t, 0)
}

func TestV3WatchMultipleEventsTxnUnsynced(t *testing.T) {
	integration.BeforeTest(t)
	testV3WatchMultipleEventsTxn(t, 1)
}

// testV3WatchMultipleEventsTxn tests Watch APIs when it receives multiple events.
func testV3WatchMultipleEventsTxn(t *testing.T, startRev int64) {
	clus := integration.NewCluster(t, &integration.ClusterConfig{Size: 3})
	defer clus.Terminate(t)

	ctx, cancel := context.WithTimeout(t.Context(), 30*time.Second)
	defer cancel()
	wStream, wErr := integration.ToGRPC(clus.RandClient()).Watch.Watch(ctx)
	if wErr != nil {
		t.Fatalf("wAPI.Watch error: %v", wErr)
	}

	wreq := &pb.WatchRequest{RequestUnion: &pb.WatchRequest_CreateRequest{
		CreateRequest: &pb.WatchCreateRequest{
			Key: []byte("foo"), RangeEnd: []byte("fop"), StartRevision: startRev,
		},
	}}
	if err := wStream.Send(wreq); err != nil {
		t.Fatalf("wStream.Send error: %v", err)
	}
	if resp, err := wStream.Recv(); err != nil || !resp.Created {
		t.Fatalf("create response failed: resp=%v, err=%v", resp, err)
	}

	kvc := integration.ToGRPC(clus.RandClient()).KV
	txn := pb.TxnRequest{}
	for i := 0; i < 3; i++ {
		ru := &pb.RequestOp{}
		ru.Request = &pb.RequestOp_RequestPut{
			RequestPut: &pb.PutRequest{
				Key: []byte(fmt.Sprintf("foo%d", i)), Value: []byte("bar"),
			},
		}
		txn.Success = append(txn.Success, ru)
	}

	tresp, err := kvc.Txn(t.Context(), &txn)
	if err != nil {
		t.Fatalf("kvc.Txn error: %v", err)
	}
	if !tresp.Succeeded {
		t.Fatalf("kvc.Txn failed: %+v", tresp)
	}

	var events []*mvccpb.Event
	for len(events) < 3 {
		resp, err := wStream.Recv()
		if err != nil {
			t.Errorf("wStream.Recv error: %v", err)
		}
		events = append(events, resp.Events...)
	}
	sort.Sort(eventsSortByKey(events))

	wevents := []*mvccpb.Event{
		{
			Type: mvccpb.PUT,
			Kv:   &mvccpb.KeyValue{Key: []byte("foo0"), Value: []byte("bar"), CreateRevision: 2, ModRevision: 2, Version: 1},
		},
		{
			Type: mvccpb.PUT,
			Kv:   &mvccpb.KeyValue{Key: []byte("foo1"), Value: []byte("bar"), CreateRevision: 2, ModRevision: 2, Version: 1},
		},
		{
			Type: mvccpb.PUT,
			Kv:   &mvccpb.KeyValue{Key: []byte("foo2"), Value: []byte("bar"), CreateRevision: 2, ModRevision: 2, Version: 1},
		},
	}

	if !reflect.DeepEqual(events, wevents) {
		t.Errorf("events got = %+v, want = %+v", events, wevents)
	}

	rok, nr := waitResponse(wStream, 1*time.Second)
	if !rok {
		t.Errorf("unexpected pb.WatchResponse is received %+v", nr)
	}
}

type eventsSortByKey []*mvccpb.Event

func (evs eventsSortByKey) Len() int      { return len(evs) }
func (evs eventsSortByKey) Swap(i, j int) { evs[i], evs[j] = evs[j], evs[i] }
func (evs eventsSortByKey) Less(i, j int) bool {
	return bytes.Compare(evs[i].Kv.Key, evs[j].Kv.Key) < 0
}

func TestV3WatchMultipleEventsPutUnsynced(t *testing.T) {
	integration.BeforeTest(t)
	clus := integration.NewCluster(t, &integration.ClusterConfig{Size: 3})
	defer clus.Terminate(t)

	kvc := integration.ToGRPC(clus.RandClient()).KV

	if _, err := kvc.Put(t.Context(), &pb.PutRequest{Key: []byte("foo0"), Value: []byte("bar")}); err != nil {
		t.Fatalf("couldn't put key (%v)", err)
	}
	if _, err := kvc.Put(t.Context(), &pb.PutRequest{Key: []byte("foo1"), Value: []byte("bar")}); err != nil {
		t.Fatalf("couldn't put key (%v)", err)
	}

	ctx, cancel := context.WithTimeout(t.Context(), 30*time.Second)
	defer cancel()
	wStream, wErr := integration.ToGRPC(clus.RandClient()).Watch.Watch(ctx)
	if wErr != nil {
		t.Fatalf("wAPI.Watch error: %v", wErr)
	}

	wreq := &pb.WatchRequest{RequestUnion: &pb.WatchRequest_CreateRequest{
		CreateRequest: &pb.WatchCreateRequest{
			Key: []byte("foo"), RangeEnd: []byte("fop"), StartRevision: 1,
		},
	}}
	if err := wStream.Send(wreq); err != nil {
		t.Fatalf("wStream.Send error: %v", err)
	}

	if _, err := kvc.Put(t.Context(), &pb.PutRequest{Key: []byte("foo0"), Value: []byte("bar")}); err != nil {
		t.Fatalf("couldn't put key (%v)", err)
	}
	if _, err := kvc.Put(t.Context(), &pb.PutRequest{Key: []byte("foo1"), Value: []byte("bar")}); err != nil {
		t.Fatalf("couldn't put key (%v)", err)
	}

	allWevents := []*mvccpb.Event{
		{
			Type: mvccpb.PUT,
			Kv:   &mvccpb.KeyValue{Key: []byte("foo0"), Value: []byte("bar"), CreateRevision: 2, ModRevision: 2, Version: 1},
		},
		{
			Type: mvccpb.PUT,
			Kv:   &mvccpb.KeyValue{Key: []byte("foo1"), Value: []byte("bar"), CreateRevision: 3, ModRevision: 3, Version: 1},
		},
		{
			Type: mvccpb.PUT,
			Kv:   &mvccpb.KeyValue{Key: []byte("foo0"), Value: []byte("bar"), CreateRevision: 2, ModRevision: 4, Version: 2},
		},
		{
			Type: mvccpb.PUT,
			Kv:   &mvccpb.KeyValue{Key: []byte("foo1"), Value: []byte("bar"), CreateRevision: 3, ModRevision: 5, Version: 2},
		},
	}

	var events []*mvccpb.Event
	for len(events) < 4 {
		resp, err := wStream.Recv()
		if err != nil {
			t.Errorf("wStream.Recv error: %v", err)
		}
		if resp.Created {
			continue
		}
		events = append(events, resp.Events...)
		// if PUT requests are committed by now, first receive would return
		// multiple events, but if not, it returns a single event. In SSD,
		// it should return 4 events at once.
	}

	if !reflect.DeepEqual(events, allWevents) {
		t.Errorf("events got = %+v, want = %+v", events, allWevents)
	}

	rok, nr := waitResponse(wStream, 1*time.Second)
	if !rok {
		t.Errorf("unexpected pb.WatchResponse is received %+v", nr)
	}
}

// TestV3WatchProgressOnMemberRestart verifies the client side doesn't
// receive duplicated events.
// Refer to https://github.com/etcd-io/etcd/pull/15248#issuecomment-1423225742.
func TestV3WatchProgressOnMemberRestart(t *testing.T) {
	integration.BeforeTest(t)

	clus := integration.NewCluster(t, &integration.ClusterConfig{
		Size:                        1,
		WatchProgressNotifyInterval: time.Second,
	})
	defer clus.Terminate(t)

	client := clus.RandClient()
	ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
	defer cancel()

	errC := make(chan error, 1)
	watchReady := make(chan struct{}, 1)
	doneC := make(chan struct{}, 1)
	progressNotifyC := make(chan struct{}, 1)
	go func() {
		defer close(doneC)

		var (
			lastWatchedModRevision  int64
			gotProgressNotification bool
		)

		wch := client.Watch(ctx, "foo", clientv3.WithProgressNotify())
		watchReady <- struct{}{}
		for wr := range wch {
			if wr.Err() != nil {
				errC <- fmt.Errorf("watch error: %w", wr.Err())
				return
			}

			if len(wr.Events) == 0 {
				// We need to make sure at least one progress notification
				// is received after receiving the normal watch response
				// and before restarting the member.
				if lastWatchedModRevision > 0 {
					gotProgressNotification = true
					progressNotifyC <- struct{}{}
				}
				continue
			}

			for _, event := range wr.Events {
				if event.Kv.ModRevision <= lastWatchedModRevision {
					errC <- fmt.Errorf("got an unexpected revision: %d, lastWatchedModRevision: %d",
						event.Kv.ModRevision,
						lastWatchedModRevision)
					return
				}
				lastWatchedModRevision = event.Kv.ModRevision
			}

			if gotProgressNotification {
				return
			}
		}
	}()

	// waiting for the watcher ready
	t.Log("Waiting for the watcher to be ready.")
	<-watchReady
	time.Sleep(time.Second)

	// write a K/V firstly
	t.Log("Writing key 'foo' firstly")
	_, err := client.Put(ctx, "foo", "bar1")
	require.NoError(t, err)

	// make sure at least one progress notification is received
	// before restarting the member
	t.Log("Waiting for the progress notification")
	select {
	case <-progressNotifyC:
	case <-time.After(5 * time.Second):
		t.Log("Do not receive the progress notification in 5 seconds, move forward anyway.")
	}

	// restart the member
	t.Log("Restarting the member")
	clus.Members[0].Stop(t)
	clus.Members[0].Restart(t)
	clus.Members[0].WaitOK(t)

	// write the same key again after the member restarted
	t.Log("Writing the same key 'foo' again after restarting the member")
	_, err = client.Put(ctx, "foo", "bar2")
	require.NoError(t, err)

	t.Log("Waiting for result")
	select {
	case <-progressNotifyC:
		t.Log("Progress notification received")
	case err := <-errC:
		t.Fatal(err)
	case <-doneC:
		t.Log("Done")
	case <-time.After(15 * time.Second):
		t.Fatal("Timed out waiting for the response")
	}
}

func TestV3WatchMultipleStreamsSynced(t *testing.T) {
	integration.BeforeTest(t)
	testV3WatchMultipleStreams(t, 0)
}

func TestV3WatchMultipleStreamsUnsynced(t *testing.T) {
	integration.BeforeTest(t)
	testV3WatchMultipleStreams(t, 1)
}

// testV3WatchMultipleStreams tests multiple watchers on the same key on multiple streams.
func testV3WatchMultipleStreams(t *testing.T, startRev int64) {
	clus := integration.NewCluster(t, &integration.ClusterConfig{Size: 3})
	defer clus.Terminate(t)

	wAPI := integration.ToGRPC(clus.RandClient()).Watch
	kvc := integration.ToGRPC(clus.RandClient()).KV

	streams := make([]pb.Watch_WatchClient, 5)
	for i := range streams {
		ctx, cancel := context.WithTimeout(t.Context(), 30*time.Second)
		defer cancel()
		wStream, errW := wAPI.Watch(ctx)
		if errW != nil {
			t.Fatalf("wAPI.Watch error: %v", errW)
		}
		wreq := &pb.WatchRequest{RequestUnion: &pb.WatchRequest_CreateRequest{
			CreateRequest: &pb.WatchCreateRequest{
				Key: []byte("foo"), StartRevision: startRev,
			},
		}}
		if err := wStream.Send(wreq); err != nil {
			t.Fatalf("wStream.Send error: %v", err)
		}
		streams[i] = wStream
	}

	for _, wStream := range streams {
		wresp, err := wStream.Recv()
		if err != nil {
			t.Fatalf("wStream.Recv error: %v", err)
		}
		if !wresp.Created {
			t.Fatalf("wresp.Created got = %v, want = true", wresp.Created)
		}
	}

	if _, err := kvc.Put(t.Context(), &pb.PutRequest{Key: []byte("foo"), Value: []byte("bar")}); err != nil {
		t.Fatalf("couldn't put key (%v)", err)
	}

	var wg sync.WaitGroup
	wg.Add(len(streams))
	wevents := []*mvccpb.Event{
		{
			Type: mvccpb.PUT,
			Kv:   &mvccpb.KeyValue{Key: []byte("foo"), Value: []byte("bar"), CreateRevision: 2, ModRevision: 2, Version: 1},
		},
	}
	for i := range streams {
		go func(i int) {
			defer wg.Done()
			wStream := streams[i]
			wresp, err := wStream.Recv()
			if err != nil {
				t.Errorf("wStream.Recv error: %v", err)
			}
			if wresp.WatchId != 0 {
				t.Errorf("watchId got = %d, want = 0", wresp.WatchId)
			}
			if !reflect.DeepEqual(wresp.Events, wevents) {
				t.Errorf("wresp.Events got = %+v, want = %+v", wresp.Events, wevents)
			}
			// now Recv should block because there is no more events coming
			rok, nr := waitResponse(wStream, 1*time.Second)
			if !rok {
				t.Errorf("unexpected pb.WatchResponse is received %+v", nr)
			}
		}(i)
	}
	wg.Wait()
}

// waitResponse waits on the given stream for given duration.
// If there is no more events, true and a nil response will be
// returned closing the WatchClient stream. Or the response will
// be returned.
func waitResponse(wc pb.Watch_WatchClient, timeout time.Duration) (bool, *pb.WatchResponse) {
	rCh := make(chan *pb.WatchResponse, 1)
	donec := make(chan struct{})
	defer close(donec)
	go func() {
		resp, _ := wc.Recv()
		select {
		case rCh <- resp:
		case <-donec:
		}
	}()
	select {
	case nr := <-rCh:
		return false, nr
	case <-time.After(timeout):
	}
	// didn't get response
	wc.CloseSend()
	return true, nil
}

func TestWatchWithProgressNotify(t *testing.T) {
	// accelerate report interval so test terminates quickly
	oldpi := v3rpc.GetProgressReportInterval()
	// using atomics to avoid race warnings
	v3rpc.SetProgressReportInterval(3 * time.Second)
	testInterval := 3 * time.Second
	defer func() { v3rpc.SetProgressReportInterval(oldpi) }()

	integration.BeforeTest(t)
	clus := integration.NewCluster(t, &integration.ClusterConfig{Size: 3})
	defer clus.Terminate(t)

	ctx, cancel := context.WithTimeout(t.Context(), 30*time.Second)
	defer cancel()
	wStream, wErr := integration.ToGRPC(clus.RandClient()).Watch.Watch(ctx)
	if wErr != nil {
		t.Fatalf("wAPI.Watch error: %v", wErr)
	}

	// create two watchers, one with progressNotify set.
	wreq := &pb.WatchRequest{RequestUnion: &pb.WatchRequest_CreateRequest{
		CreateRequest: &pb.WatchCreateRequest{Key: []byte("foo"), StartRevision: 1, ProgressNotify: true},
	}}
	if err := wStream.Send(wreq); err != nil {
		t.Fatalf("watch request failed (%v)", err)
	}
	wreq = &pb.WatchRequest{RequestUnion: &pb.WatchRequest_CreateRequest{
		CreateRequest: &pb.WatchCreateRequest{Key: []byte("foo"), StartRevision: 1},
	}}
	if err := wStream.Send(wreq); err != nil {
		t.Fatalf("watch request failed (%v)", err)
	}

	// two creation  + one notification
	for i := 0; i < 3; i++ {
		rok, resp := waitResponse(wStream, testInterval+time.Second)
		if resp.Created {
			continue
		}

		if rok {
			t.Errorf("failed to receive response from watch stream")
		}
		if resp.Header.Revision != 1 {
			t.Errorf("revision = %d, want 1", resp.Header.Revision)
		}
		if len(resp.Events) != 0 {
			t.Errorf("len(resp.Events) = %d, want 0", len(resp.Events))
		}
	}

	// no more notification
	rok, resp := waitResponse(wStream, time.Second)
	if !rok {
		t.Errorf("unexpected pb.WatchResponse is received %+v", resp)
	}
}

// TestV3WatchClose opens many watchers concurrently on multiple streams.
func TestV3WatchClose(t *testing.T) {
	integration.BeforeTest(t)
	clus := integration.NewCluster(t, &integration.ClusterConfig{Size: 1, UseBridge: true})
	defer clus.Terminate(t)

	c := clus.Client(0)
	wapi := integration.ToGRPC(c).Watch

	var wg sync.WaitGroup
	wg.Add(100)
	for i := 0; i < 100; i++ {
		go func() {
			ctx, cancel := context.WithCancel(t.Context())
			defer func() {
				wg.Done()
				cancel()
			}()
			ws, err := wapi.Watch(ctx)
			if err != nil {
				return
			}
			cr := &pb.WatchCreateRequest{Key: []byte("a")}
			req := &pb.WatchRequest{
				RequestUnion: &pb.WatchRequest_CreateRequest{
					CreateRequest: cr,
				},
			}
			ws.Send(req)
			ws.Recv()
		}()
	}

	clus.Members[0].Bridge().DropConnections()
	wg.Wait()
}

// TestV3WatchWithFilter ensures watcher filters out the events correctly.
func TestV3WatchWithFilter(t *testing.T) {
	integration.BeforeTest(t)

	clus := integration.NewCluster(t, &integration.ClusterConfig{Size: 1})
	defer clus.Terminate(t)

	ctx, cancel := context.WithTimeout(t.Context(), 30*time.Second)
	defer cancel()

	ws, werr := integration.ToGRPC(clus.RandClient()).Watch.Watch(ctx)
	require.NoError(t, werr)
	req := &pb.WatchRequest{RequestUnion: &pb.WatchRequest_CreateRequest{
		CreateRequest: &pb.WatchCreateRequest{
			Key:     []byte("foo"),
			Filters: []pb.WatchCreateRequest_FilterType{pb.WatchCreateRequest_NOPUT},
		},
	}}
	require.NoError(t, ws.Send(req))
	_, err := ws.Recv()
	require.NoError(t, err)

	recv := make(chan *pb.WatchResponse, 1)
	go func() {
		// check received PUT
		resp, rerr := ws.Recv()
		if rerr != nil {
			t.Error(rerr)
		}
		recv <- resp
	}()

	// put a key with empty value
	kvc := integration.ToGRPC(clus.RandClient()).KV
	preq := &pb.PutRequest{Key: []byte("foo")}
	_, err = kvc.Put(t.Context(), preq)
	require.NoError(t, err)

	select {
	case <-recv:
		t.Fatal("failed to filter out put event")
	case <-time.After(100 * time.Millisecond):
	}

	dreq := &pb.DeleteRangeRequest{Key: []byte("foo")}
	_, err = kvc.DeleteRange(t.Context(), dreq)
	require.NoError(t, err)

	select {
	case resp := <-recv:
		wevs := []*mvccpb.Event{
			{
				Type: mvccpb.DELETE,
				Kv:   &mvccpb.KeyValue{Key: []byte("foo"), ModRevision: 3},
			},
		}
		if !reflect.DeepEqual(resp.Events, wevs) {
			t.Fatalf("got %v, expected %v", resp.Events, wevs)
		}
	case <-time.After(100 * time.Millisecond):
		t.Fatal("failed to receive delete event")
	}
}

func TestV3WatchWithPrevKV(t *testing.T) {
	integration.BeforeTest(t)
	clus := integration.NewCluster(t, &integration.ClusterConfig{Size: 1})
	defer clus.Terminate(t)

	wctx, wcancel := context.WithCancel(t.Context())
	defer wcancel()

	tests := []struct {
		key  string
		end  string
		vals []string
	}{{
		key:  "foo",
		end:  "fop",
		vals: []string{"bar1", "bar2"},
	}, {
		key:  "/abc",
		end:  "/abd",
		vals: []string{"first", "second"},
	}}
	for i, tt := range tests {
		kvc := integration.ToGRPC(clus.RandClient()).KV
		_, err := kvc.Put(t.Context(), &pb.PutRequest{Key: []byte(tt.key), Value: []byte(tt.vals[0])})
		require.NoError(t, err)

		ws, werr := integration.ToGRPC(clus.RandClient()).Watch.Watch(wctx)
		require.NoError(t, werr)

		req := &pb.WatchRequest{RequestUnion: &pb.WatchRequest_CreateRequest{
			CreateRequest: &pb.WatchCreateRequest{
				Key:      []byte(tt.key),
				RangeEnd: []byte(tt.end),
				PrevKv:   true,
			},
		}}
		err = ws.Send(req)
		require.NoError(t, err)
		_, err = ws.Recv()
		require.NoError(t, err)

		_, err = kvc.Put(t.Context(), &pb.PutRequest{Key: []byte(tt.key), Value: []byte(tt.vals[1])})
		require.NoError(t, err)

		recv := make(chan *pb.WatchResponse, 1)
		go func() {
			// check received PUT
			resp, rerr := ws.Recv()
			if rerr != nil {
				t.Error(rerr)
			}
			recv <- resp
		}()

		select {
		case resp := <-recv:
			if tt.vals[1] != string(resp.Events[0].Kv.Value) {
				t.Errorf("#%d: unequal value: want=%s, get=%s", i, tt.vals[1], resp.Events[0].Kv.Value)
			}
			if tt.vals[0] != string(resp.Events[0].PrevKv.Value) {
				t.Errorf("#%d: unequal value: want=%s, get=%s", i, tt.vals[0], resp.Events[0].PrevKv.Value)
			}
		case <-time.After(30 * time.Second):
			t.Error("timeout waiting for watch response")
		}
	}
}

// TestV3WatchCancellation ensures that watch cancellation frees up server resources.
func TestV3WatchCancellation(t *testing.T) {
	integration.BeforeTest(t)

	clus := integration.NewCluster(t, &integration.ClusterConfig{Size: 1})
	defer clus.Terminate(t)

	ctx, cancel := context.WithTimeout(t.Context(), 30*time.Second)
	defer cancel()

	cli := clus.RandClient()

	// increment watcher total count and keep a stream open
	cli.Watch(ctx, "/foo")

	for i := 0; i < 1000; i++ {
		wctx, wcancel := context.WithCancel(ctx)
		cli.Watch(wctx, "/foo")
		wcancel()
	}

	// Wait a little for cancellations to take hold
	time.Sleep(3 * time.Second)

	minWatches, err := clus.Members[0].Metric("etcd_debugging_mvcc_watcher_total")
	require.NoError(t, err)

	var expected string
	if integration.ThroughProxy {
		// grpc proxy has additional 2 watches open
		expected = "3"
	} else {
		expected = "1"
	}

	if minWatches != expected {
		t.Fatalf("expected %s watch, got %s", expected, minWatches)
	}
}

// TestV3WatchCloseCancelRace ensures that watch close doesn't decrement the watcher total too far.
func TestV3WatchCloseCancelRace(t *testing.T) {
	integration.BeforeTest(t)

	clus := integration.NewCluster(t, &integration.ClusterConfig{Size: 1})
	defer clus.Terminate(t)

	ctx, cancel := context.WithTimeout(t.Context(), 30*time.Second)
	defer cancel()

	cli := clus.RandClient()

	for i := 0; i < 1000; i++ {
		wctx, wcancel := context.WithCancel(ctx)
		cli.Watch(wctx, "/foo")
		wcancel()
	}

	// Wait a little for cancellations to take hold
	time.Sleep(3 * time.Second)

	minWatches, err := clus.Members[0].Metric("etcd_debugging_mvcc_watcher_total")
	require.NoError(t, err)

	var expected string
	if integration.ThroughProxy {
		// grpc proxy has additional 2 watches open
		expected = "2"
	} else {
		expected = "0"
	}

	if minWatches != expected {
		t.Fatalf("expected %s watch, got %s", expected, minWatches)
	}
}

// TestV3WatchProgressWaitsForSync checks that progress notifications
// don't get sent until the watcher is synchronised
func TestV3WatchProgressWaitsForSync(t *testing.T) {
	// Disable for gRPC proxy, as it does not support requesting
	// progress notifications
	if integration.ThroughProxy {
		t.Skip("grpc proxy currently does not support requesting progress notifications")
	}

	integration.BeforeTest(t)

	clus := integration.NewCluster(t, &integration.ClusterConfig{Size: 1})
	defer clus.Terminate(t)

	client := clus.RandClient()
	ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
	defer cancel()

	// Write a couple values into key to make sure there's a
	// non-trivial amount of history.
	count := 1001
	t.Logf("Writing key 'foo' %d times", count)
	for i := 0; i < count; i++ {
		_, err := client.Put(ctx, "foo", fmt.Sprintf("bar%d", i))
		require.NoError(t, err)
	}

	// Create watch channel starting at revision 1 (i.e. it starts
	// unsynced because of the update above)
	wch := client.Watch(ctx, "foo", clientv3.WithRev(1))

	// Immediately request a progress notification. As the client
	// is unsynchronised, the server will not sent any notification,
	// as client can infer progress from events.
	err := client.RequestProgress(ctx)
	require.NoError(t, err)

	// Verify that we get the watch responses first. Note that
	// events might be spread across multiple packets.
	eventCount := 0
	for eventCount < count {
		wr := <-wch
		if wr.Err() != nil {
			t.Fatal(fmt.Errorf("watch error: %w", wr.Err()))
		}
		if wr.IsProgressNotify() {
			t.Fatal("Progress notification from unsynced client!")
		}
		if wr.Header.Revision != int64(count+1) {
			t.Fatal("Incomplete watch response!")
		}
		eventCount += len(wr.Events)
	}
	// client needs to request progress notification again
	err = client.RequestProgress(ctx)
	require.NoError(t, err)
	wr2 := <-wch
	if wr2.Err() != nil {
		t.Fatal(fmt.Errorf("watch error: %w", wr2.Err()))
	}
	if !wr2.IsProgressNotify() {
		t.Fatal("Did not receive progress notification!")
	}
	if wr2.Header.Revision != int64(count+1) {
		t.Fatal("Wrong revision in progress notification!")
	}
}

func TestV3WatchProgressWaitsForSyncNoEvents(t *testing.T) {
	if integration.ThroughProxy {
		t.Skip("grpc proxy currently does not support requesting progress notifications")
	}
	integration.BeforeTest(t)

	clus := integration.NewCluster(t, &integration.ClusterConfig{Size: 1})
	defer clus.Terminate(t)

	client := clus.RandClient()
	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	defer cancel()

	resp, err := client.Put(ctx, "bar", "1")
	require.NoError(t, err)

	wch := client.Watch(ctx, "foo", clientv3.WithRev(resp.Header.Revision))
	// Request the progress notification on newly created watch that was not yet synced.
	err = client.RequestProgress(ctx)
	ticker := time.NewTicker(100 * time.Millisecond)
	defer ticker.Stop()

	require.NoError(t, err)
	gotProgressNotification := false
	for {
		select {
		case <-ticker.C:
			err := client.RequestProgress(ctx)
			require.NoError(t, err)
		case resp := <-wch:
			if resp.Err() != nil {
				t.Fatal(fmt.Errorf("watch error: %w", resp.Err()))
			}
			if resp.IsProgressNotify() {
				gotProgressNotification = true
			}
		}
		if gotProgressNotification {
			break
		}
	}
	require.Truef(t, gotProgressNotification, "Expected to get progress notification")
}

// TestV3NoEventsLostOnCompact verifies that slow watchers exit with compacted watch response
// if its next revision of events are compacted and no lost events sent to client.
func TestV3NoEventsLostOnCompact(t *testing.T) {
	if integration.ThroughProxy {
		t.Skip("grpc proxy currently does not support requesting progress notifications")
	}
	integration.BeforeTest(t)
	if len(gofail.List()) == 0 {
		t.Skip("please run 'make gofail-enable' before running the test")
	}
	clus := integration.NewCluster(t, &integration.ClusterConfig{Size: 1})
	defer clus.Terminate(t)

	client := clus.RandClient()
	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	defer cancel()

	// sendLoop throughput is rate-limited to 1 event per second
	require.NoError(t, gofail.Enable("beforeSendWatchResponse", `sleep("1s")`))
	wch := client.Watch(ctx, "foo")

	var rev int64
	writeCount := mvcc.ChanBufLen() * 11 / 10
	for i := 0; i < writeCount; i++ {
		resp, err := client.Put(ctx, "foo", "bar")
		require.NoError(t, err)
		rev = resp.Header.Revision
	}
	_, err := client.Compact(ctx, rev)
	require.NoError(t, err)

	time.Sleep(time.Second)
	require.NoError(t, gofail.Disable("beforeSendWatchResponse"))

	eventCount := 0
	compacted := false
	for resp := range wch {
		err = resp.Err()
		if err != nil {
			if !errors.Is(err, rpctypes.ErrCompacted) {
				t.Fatalf("want watch response err %v but got %v", rpctypes.ErrCompacted, err)
			}
			compacted = true
			break
		}
		eventCount += len(resp.Events)
		if eventCount == writeCount {
			break
		}
	}
	assert.Truef(t, compacted, "Expected stream to get compacted, instead we got %d events out of %d events", eventCount, writeCount)
}
