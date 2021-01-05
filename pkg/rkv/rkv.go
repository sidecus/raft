package rkv

import (
	"sync"

	"github.com/sidecus/raft/pkg/raft"
	"github.com/sidecus/raft/pkg/util"
)

// StartRKV starts the raft kv store and waits for it to finish
// nodeID: id for current node
// port: port for current node
// peers: info for all other nodes
func StartRKV(nodeID int, port string, peers map[int]raft.NodeInfo) {
	// create node
	node, err := raft.NewNode(nodeID, peers, newRKVStore(), rkvProxyFactory)
	if err != nil {
		util.Fatalf("%s\n", err)
	}

	// create rpc server
	var wg sync.WaitGroup
	rpcServer := newRKVRPCServer(node, &wg)

	// start
	rpcServer.Start(port)
	node.Start()
	wg.Wait()
}
