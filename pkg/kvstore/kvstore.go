package kvstore

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"sync"

	"github.com/sidecus/raft/pkg/raft"
	"github.com/sidecus/raft/pkg/util"
)

// KVStore implements raft.IStateMachine

var errorNoKeyProvidedForGet = errors.New("no key provided for Get")

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
	if cmd.CmdType != KVCmdSet && cmd.CmdType != KVCmdDel {
		util.Panicf("Unexpected kv cmdtype %d", cmd.CmdType)
	}

	store.mu.Lock()
	defer store.mu.Unlock()

	data := cmd.Data.(KVCmdData)
	if cmd.CmdType == KVCmdSet {
		store.data[data.Key] = data.Value
	} else if cmd.CmdType == KVCmdDel {
		delete(store.data, data.Key)
	}
}

// Get Implements IStateMachine.Get
func (store *KVStore) Get(param ...interface{}) (result interface{}, err error) {
	if len(param) != 1 {
		return nil, errorNoKeyProvidedForGet
	}

	store.mu.RLock()
	defer store.mu.RUnlock()

	key := param[0].(string)
	if v, ok := store.data[key]; ok {
		return v, nil
	}

	return "", fmt.Errorf("Key %s doesn't exist", key)
}

// Serialize implements IStateMachine.TakeSnapshot
func (store *KVStore) Serialize(w io.Writer) error {
	store.mu.RLock()
	defer store.mu.RUnlock()

	// we use JSON serialized data for our kv store
	return json.NewEncoder(w).Encode(store.data)
}

// Deserialize installs a snapshot, it implements IStateMachine.InstallSnapshot
func (store *KVStore) Deserialize(reader io.Reader) error {
	store.mu.Lock()
	defer store.mu.Unlock()

	return json.NewDecoder(reader).Decode(&store.data)
}
