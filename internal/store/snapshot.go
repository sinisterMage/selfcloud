package store

import (
	"bytes"
	"encoding/gob"
	"fmt"

	bolt "go.etcd.io/bbolt"
)

// snapshotData is a flat dump of every bucket -> key -> value.
type snapshotData struct {
	Buckets map[string]map[string][]byte
}

func dumpBolt(s *Store) ([]byte, error) {
	out := snapshotData{Buckets: map[string]map[string][]byte{}}
	err := s.db.View(func(tx *bolt.Tx) error {
		return tx.ForEach(func(name []byte, b *bolt.Bucket) error {
			m := map[string][]byte{}
			err := b.ForEach(func(k, v []byte) error {
				kc := make([]byte, len(k))
				copy(kc, k)
				vc := make([]byte, len(v))
				copy(vc, v)
				m[string(kc)] = vc
				return nil
			})
			if err != nil {
				return err
			}
			out.Buckets[string(name)] = m
			return nil
		})
	})
	if err != nil {
		return nil, err
	}
	var buf bytes.Buffer
	enc := gob.NewEncoder(&buf)
	if err := enc.Encode(out); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

func restoreBolt(s *Store, data []byte) error {
	var snap snapshotData
	dec := gob.NewDecoder(bytes.NewReader(data))
	if err := dec.Decode(&snap); err != nil {
		return fmt.Errorf("decode snapshot: %w", err)
	}
	return s.db.Update(func(tx *bolt.Tx) error {
		for name, kvs := range snap.Buckets {
			if err := tx.DeleteBucket([]byte(name)); err != nil && err != bolt.ErrBucketNotFound {
				return err
			}
			b, err := tx.CreateBucket([]byte(name))
			if err != nil {
				return err
			}
			for k, v := range kvs {
				if err := b.Put([]byte(k), v); err != nil {
					return err
				}
			}
		}
		return nil
	})
}
