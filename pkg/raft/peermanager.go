package raft

import (
	"errors"
	"sync"

	"github.com/sidecus/raft/pkg/util"
)

const nextIndexFallbackStep = 20

var errorNoPeersProvided = errors.New("No raft peers provided")
var errorInvalidNodeID = errors.New("Invalid node id")

// IPeerProxy defines the RPC client interface for a specific peer nodes
// It's an abstraction layer so that concrete implementation (RPC or REST) can be decoupled from this package
type IPeerProxy interface {
	// AppendEntries calls a peer node to append entries.
	// interface implementation needs to ensure onReply is called regardless of whether the called failed or not. On failure, call onReply with nil
	AppendEntries(req *AppendEntriesRequest) (*AppendEntriesReply, error)

	// RequestVote calls a peer node to vote.
	// interface implementation needs to ensure onReply is called regardless of whether the called failed or not. On failure, call onReply with nil
	RequestVote(req *RequestVoteRequest) (*RequestVoteReply, error)

	// InstallSnapshot calls a peer node to install a snapshot.
	// interface implementation needs to ensure onReply is called regardless of whether the called failed or not. On failure, call onReply with nil
	InstallSnapshot(req *SnapshotRequest) (*AppendEntriesReply, error)

	// Get invokes a peer node to get values
	Get(req *GetRequest) (*GetReply, error)

	// Execute invokes a node (usually the leader) to do set or delete operations
	Execute(cmd *StateMachineCmd) (*ExecuteReply, error)
}

// IPeerProxyFactory creates a new proxy
type IPeerProxyFactory interface {
	// factory method
	NewPeerProxy(info NodeInfo) IPeerProxy
}

// Peer wraps information for a raft Peer as well as the RPC proxy
type Peer struct {
	NodeInfo
	nextIndex      int
	matchIndex     int
	ReplicationSig chan interface{}

	IPeerProxy
}

// HasMatch tells us whether we have found a matching entry for the given follower
func (p *Peer) HasMatch() bool {
	return p.matchIndex+1 == p.nextIndex
}

// HasMoreToReplicate tells us whether there are more to replicate for this follower
func (p *Peer) HasMoreToReplicate(lastIndex int) bool {
	return p.matchIndex < lastIndex
}

// UpdateMatchIndex updates match index for a given node
func (p *Peer) UpdateMatchIndex(match bool, lastMatch int) {
	if match {
		if p.matchIndex != lastMatch {
			util.WriteVerbose("Updating Node%d's nextIndex. lastMatch %d", p.NodeID, lastMatch)
			p.nextIndex = lastMatch + 1
			p.matchIndex = lastMatch
		}
	} else {
		util.WriteVerbose("Decreasing Node%d's nextIndex. lastMatch %d", p.NodeID, lastMatch)
		// prev entries don't match. decrement nextIndex.
		// cap it to 0. It is meaningless when less than zero
		p.nextIndex = util.Max(0, p.nextIndex-nextIndexFallbackStep)
		p.matchIndex = -1
	}
}

// TriggerReplication triggers replication for current node
func (p *Peer) TriggerReplication() {
	p.ReplicationSig <- struct{}{}
}

// IPeerManager defines raft peer manager interface.
type IPeerManager interface {
	GetPeers() map[int]*Peer
	GetPeer(nodeID int) *Peer
	RunAndWaitAllPeers(action func(*Peer) interface{}) chan interface{}

	ResetFollowerIndicies(lastLogIndex int)
	QuorumReached(logIndex int) bool

	Start()
	Stop()
}

// ReplicateFunc function type used to replicate data
type ReplicateFunc func(followerID int)

// PeerManager manages communication with peers
type PeerManager struct {
	Peers     map[int]*Peer
	ChStop    chan interface{}
	Replicate ReplicateFunc
	wg        sync.WaitGroup
}

// NewPeerManager creates the node proxy for kv store
func NewPeerManager(nodeID int, peers map[int]NodeInfo, replicate ReplicateFunc, factory IPeerProxyFactory) IPeerManager {
	if len(peers) == 0 {
		util.Panicln(errorNoPeersProvided)
	}

	if _, ok := peers[nodeID]; ok {
		util.Panicf("current node %d is listed in peers\n", nodeID)
	}

	mgr := &PeerManager{
		Peers:     make(map[int]*Peer),
		Replicate: replicate,
		ChStop:    make(chan interface{}),
	}

	// Initialize each peer
	for _, info := range peers {
		mgr.Peers[info.NodeID] = &Peer{
			NodeInfo:       info,
			nextIndex:      0,
			matchIndex:     -1,
			ReplicationSig: make(chan interface{}, 20),
			IPeerProxy:     factory.NewPeerProxy(info),
		}
	}

	return mgr
}

// GetPeer gets the peer for a given node id
func (mgr *PeerManager) GetPeer(nodeID int) *Peer {
	peer, ok := mgr.Peers[nodeID]
	if !ok {
		util.Panicln(errorInvalidNodeID)
	}

	return peer
}

// GetPeers returns all the peers
func (mgr *PeerManager) GetPeers() map[int]*Peer {
	return mgr.Peers
}

// ResetFollowerIndicies resets all follower's indices based on lastLogIndex
func (mgr *PeerManager) ResetFollowerIndicies(lastLogIndex int) {
	for _, p := range mgr.Peers {
		p.nextIndex = lastLogIndex + 1
		p.matchIndex = -1
	}
}

// QuorumReached tells whether we have majority of the followers match the given logIndex
func (mgr *PeerManager) QuorumReached(logIndex int) bool {
	// both match count and majority should include the leader itself, which is not part of the peerManager
	matchCnt := 1
	quorum := (len(mgr.Peers) + 1) / 2
	for _, p := range mgr.Peers {
		if p.matchIndex >= logIndex {
			matchCnt++
			if matchCnt > quorum {
				return true
			}
		}
	}

	return false
}

// Start starts a replication goroutine for each follower
func (mgr *PeerManager) Start() {
	mgr.wg.Add(len(mgr.Peers))

	for _, p := range mgr.Peers {
		go func(follower *Peer) {
			stop := false
			for !stop {
				select {
				case <-follower.ReplicationSig:
					mgr.Replicate(follower.NodeID)
				case <-mgr.ChStop:
					stop = true
					break
				}
			}
			mgr.wg.Done()
		}(p)
	}
}

// Stop stops the replication goroutines
func (mgr *PeerManager) Stop() {
	close(mgr.ChStop)
	mgr.wg.Wait()
}

// RunAndWaitAllPeers Run an action against all peers and wait for response
// This function returns a channel of objects generated by the action against each node
// Note number of objects in the channel doesn't have to be the same as number of peers - e.g. some peer failed
func (mgr *PeerManager) RunAndWaitAllPeers(action func(*Peer) interface{}) chan interface{} {
	peers := mgr.GetPeers()
	count := len(peers)
	replies := make(chan interface{}, count)

	var wg sync.WaitGroup

	wg.Add(count)
	for _, p := range peers {
		go func(peer *Peer) {
			ret := action(peer)
			if ret != nil {
				replies <- ret
			}
			wg.Done()
		}(p)
	}
	wg.Wait()

	// close replies and return
	close(replies)
	return replies
}
