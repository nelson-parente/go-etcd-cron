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

// Forked from ETCD to include WithPrevKV in the SyncUpdates function.

// Package mirror implements etcd mirroring operations.
package informer

import (
	"context"

	clientv3 "go.etcd.io/etcd/client/v3"
	"go.etcd.io/etcd/client/v3/mirror"

	"github.com/diagridio/go-etcd-cron/internal/client/api"
)

const (
	batchLimit = 1024
)

// newSyncer creates a Syncer.
func newSyncer(c api.Interface, prefix string, rev int64) mirror.Syncer {
	return &syncer{c: c, prefix: prefix, rev: rev}
}

type syncer struct {
	c      api.Interface
	rev    int64
	prefix string
}

func (s *syncer) SyncBase(ctx context.Context) (<-chan clientv3.GetResponse, chan error) {
	respchan := make(chan clientv3.GetResponse, 1024)
	errchan := make(chan error, 1)

	// if rev is not specified, we will choose the most recent revision.
	if s.rev == 0 {
		// If len(s.prefix) == 0, we will check a random key to fetch the most recent
		// revision (foo), otherwise we use the provided prefix.
		checkPath := "foo"
		if len(s.prefix) != 0 {
			checkPath = s.prefix
		}
		resp, err := s.c.Get(ctx, checkPath)
		if err != nil {
			errchan <- err
			close(respchan)
			close(errchan)
			return respchan, errchan
		}
		s.rev = resp.Header.Revision
	}

	go func() {
		defer close(respchan)
		defer close(errchan)

		var key string

		opts := []clientv3.OpOption{
			clientv3.WithLimit(batchLimit), clientv3.WithRev(s.rev),
			clientv3.WithSort(clientv3.SortByKey, clientv3.SortAscend),
		}

		if len(s.prefix) == 0 {
			// If len(s.prefix) == 0, we will sync the entire key-value space.
			// We then range from the smallest key (0x00) to the end.
			opts = append(opts, clientv3.WithFromKey())
			key = "\x00"
		} else {
			// If len(s.prefix) != 0, we will sync key-value space with given prefix.
			// We then range from the prefix to the next prefix if exists. Or we will
			// range from the prefix to the end if the next prefix does not exists.
			opts = append(opts, clientv3.WithRange(clientv3.GetPrefixRangeEnd(s.prefix)))
			key = s.prefix
		}

		for {
			resp, err := s.c.Get(ctx, key, opts...)
			if err != nil {
				errchan <- err
				return
			}

			respchan <- *resp

			if !resp.More {
				return
			}
			// move to next key
			key = string(append(resp.Kvs[len(resp.Kvs)-1].Key, 0))
		}
	}()

	return respchan, errchan
}

func (s *syncer) SyncUpdates(ctx context.Context) clientv3.WatchChan {
	if s.rev == 0 {
		panic("unexpected revision = 0. Calling SyncUpdates before SyncBase finishes?")
	}
	return s.c.Watch(ctx, s.prefix, clientv3.WithPrefix(), clientv3.WithRev(s.rev+1), clientv3.WithPrevKV())
}
