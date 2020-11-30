package raft

import (
	"errors"
	"fmt"
	"sync"
)

// NodeState is the state of the node
type NodeState int

const (
	//Follower state
	Follower = 1
	// Candidate state
	Candidate = 2
	// Leader state
	Leader = 3
)

// ErrorNoLeaderAvailable means there is no leader elected yet (or at least not known to current node)
var ErrorNoLeaderAvailable = errors.New("No leader currently available")

// INode represents one raft node
type INode interface {
	// Raft
	AppendEntries(*AppendEntriesRequest) (*AppendEntriesReply, error)
	RequestVote(*RequestVoteRequest) (*RequestVoteReply, error)
	OnTimer()

	// Data related
	Get(*GetRequest) (*GetReply, error)
	Execute(*StateMachineCmd) (bool, error)

	// Lifecycle
	Start()
	Stop()
}

type followerInfo struct {
	nodeID     int
	nextIndex  int
	matchIndex int
}

// node A raft node
type node struct {
	// node lock
	mu sync.RWMutex

	// data for all states
	clusterSize   int
	nodeID        int
	nodeState     NodeState
	currentTerm   int
	currentLeader int
	votedFor      int
	logMgr        *logManager
	stateMachine  IStateMachine
	proxy         IPeerProxy

	// leader only
	followerInfo map[int]*followerInfo

	// candidate only
	votes map[int]bool
}

// NewNode creates a new node
func NewNode(id int, nodeIDs []int, sm IStateMachine, proxy IPeerProxy) INode {
	size := len(nodeIDs)
	n := &node{
		mu:            sync.RWMutex{},
		clusterSize:   size,
		nodeID:        id,
		nodeState:     Follower,
		currentTerm:   0,
		currentLeader: -1,
		votedFor:      -1,
		logMgr:        newLogMgr(),
		stateMachine:  sm,
		proxy:         proxy,
		followerInfo:  make(map[int]*followerInfo, size-1),
		votes:         make(map[int]bool, size),
	}

	// Initialize follower info array
	for _, v := range nodeIDs {
		if v != id {
			n.followerInfo[v] = &followerInfo{nodeID: v, nextIndex: 0, matchIndex: -1}
		}
	}

	return n
}

// OnTimer handles a timer event.
// Action is based on node's current state
func (n *node) OnTimer() {
	n.mu.Lock()
	defer n.mu.Unlock()

	if n.nodeState == Follower || n.nodeState == Candidate {
		n.startElection()
	} else if n.nodeState == Leader {
		n.sendHeartbeat()
	}
}

// Start starts the node
func (n *node) Start() {
	n.enterFollowerState(n.nodeID, 0)
	StartRaftTimer(n)
	n.refreshTimer()
}

func (n *node) Stop() {
	// We don't wait for the goroutine to finish, just no need
	StopRaftTimer()
}

// Refreshes timer based on current state
func (n *node) refreshTimer() {
	RefreshRaftTimer(n.nodeState)
}

// enter follower state and follows new leader (or potential leader)
func (n *node) enterFollowerState(sourceNodeID, newTerm int) {
	n.nodeState = Follower
	n.currentTerm = newTerm
	n.currentLeader = sourceNodeID

	n.votedFor = -1

	fmt.Printf("T%d: Node%d follows Node%d\n", n.currentTerm, n.nodeID, sourceNodeID)
}

// enter candidate state
func (n *node) enterCandidateState() {
	n.nodeState = Candidate
	n.currentTerm++
	n.currentLeader = -1

	// vote for self first
	n.votedFor = n.nodeID
	n.votes = make(map[int]bool, n.clusterSize)
	n.votes[n.nodeID] = true

	fmt.Printf("T%d: Node%d starts election\n", n.currentTerm, n.nodeID)
}

// enterLeaderState resets leader indicies. Caller should acquire writer lock
func (n *node) enterLeaderState() {
	n.nodeState = Leader
	n.currentLeader = n.nodeID
	// reset leader indicies
	for _, v := range n.followerInfo {
		v.nextIndex = n.logMgr.lastIndex + 1
		v.matchIndex = 0
	}

	fmt.Printf("T%d: Node%d won election\n", n.currentTerm, n.nodeID)
}

// start an election, caller should acquire write lock
func (n *node) startElection() {
	n.enterCandidateState()

	// create RV request
	req := &RequestVoteRequest{
		Term:         n.currentTerm,
		CandidateID:  n.nodeID,
		LastLogIndex: n.logMgr.lastIndex,
		LastLogTerm:  n.logMgr.lastTerm,
	}

	// request vote (on different go routines), response will be processed on different go routine
	n.proxy.RequestVote(req, func(reply *RequestVoteReply) { n.handleRequestVoteReply(reply) })

	n.refreshTimer()
}

// send heartbeat, caller should acquire at least reader lock
func (n *node) sendHeartbeat() {
	// create RV request
	req := &AppendEntriesRequest{
		Term:         n.currentTerm,
		LeaderID:     n.nodeID,
		PrevLogIndex: n.logMgr.lastIndex,
		PrevLogTerm:  n.logMgr.lastTerm,
		Entries:      []LogEntry{},
		LeaderCommit: n.logMgr.commitIndex,
	}

	fmt.Printf("T%d: Node%d sending heartbeat\n", n.currentTerm, n.nodeID)

	// send heart beat (on different go routines), response will be processed there
	n.proxy.BroadcastAppendEntries(req, func(reply *AppendEntriesReply) { n.handleAppendEntriesReply(reply) })

	// 5.2 - refresh timer
	n.refreshTimer()
}

// Check peer's term and follow if it's higher. This will be called upon all RPC request and responses.
// Returns true if we are following a higher term
func (n *node) tryFollowHigherTerm(sourceNodeID, newTerm int, refreshTimerOnSameTerm bool) bool {
	ret := false
	if newTerm > n.currentTerm {
		// Treat the source node as the potential leader for the higher term
		// Till real heartbeat for that term arrives, the source node potentially knows better about the leader than us
		n.enterFollowerState(sourceNodeID, newTerm)
		n.refreshTimer()
		ret = true
	} else if newTerm == n.currentTerm && refreshTimerOnSameTerm {
		n.refreshTimer()
	}

	return ret
}

// tryReplicateLogs to follower as needed
func (n *node) tryReplicateLogs(follower *followerInfo) {
	if n.logMgr.lastIndex < follower.nextIndex {
		return
	}

	// create RV request
	nextIdx := follower.nextIndex
	prevIdx := follower.nextIndex - 1
	prevTerm := -1
	if prevIdx >= 0 {
		prevTerm = n.logMgr.logs[prevIdx].Term
	}
	req := &AppendEntriesRequest{
		Term:         n.currentTerm,
		LeaderID:     n.nodeID,
		PrevLogIndex: prevIdx,
		PrevLogTerm:  prevTerm,
		Entries:      n.logMgr.logs[nextIdx:],
		LeaderCommit: n.logMgr.commitIndex,
	}

	n.proxy.AppendEntries(
		follower.nodeID,
		req,
		func(reply *AppendEntriesReply) {
			n.handleAppendEntriesReply(reply)
		})
}
