package kvstore

import (
	"errors"
	"sync"

	"github.com/sidecus/raft/pkg/raft"
)

const (
	// KVCmdSet Set a key/value pair
	KVCmdSet = 1
	// KVCmdDel Delete a key/value pair
	KVCmdDel = 2
)

// KVCmdData represents one Key/Value command data in the log entry
type KVCmdData struct {
	Key   string
	Value string
}

// KVStore is a concurrency safe kv store
type KVStore struct {
	mu   sync.RWMutex
	data map[string]string
}

// NewKVStore creates a kv store
func NewKVStore() *KVStore {
	store := &KVStore{
		data: make(map[string]string),
	}
	return store
}

// Apply applies the cmd to the kv store with concurrency safety
func (store *KVStore) Apply(cmd raft.StateMachineCmd) {
	store.mu.Lock()
	defer store.mu.Unlock()

	store.apply(cmd)
}

// Get Implements IStateMachine.Get
func (store *KVStore) Get(param ...interface{}) (result interface{}, err error) {
	if len(param) != 1 {
		return nil, errors.New("no key provided")
	}

	key := param[0].(string)
	val, ok := store.getValue(key)
	if !ok {
		return nil, errors.New("key doesn't exist")
	}

	return val, nil
}

// getValue gets a value from store
func (store *KVStore) getValue(key string) (val string, ok bool) {
	store.mu.RLock()
	defer store.mu.RUnlock()

	val, ok = store.data[key]
	return val, ok
}

// apply applies a command to the store, parent should acquire lock
func (store *KVStore) apply(cmd raft.StateMachineCmd) {
	data := cmd.Data.(KVCmdData)
	if cmd.CmdType == KVCmdSet {
		store.data[data.Key] = data.Value
	} else if cmd.CmdType == KVCmdDel {
		delete(store.data, data.Key)
	} else {
		panic("unexpected kv cmdtype")
	}
}
