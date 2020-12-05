package raft

import "github.com/sidecus/raft/pkg/util"

// enter follower state and follows new leader (or potential leader)
func (n *node) enterFollowerState(sourceNodeID, newTerm int) {
	oldLeader := n.currentLeader
	n.nodeState = Follower
	n.currentLeader = sourceNodeID
	n.setTerm(newTerm)

	if n.nodeID != sourceNodeID && oldLeader != n.currentLeader {
		util.WriteInfo("T%d: Node%d follows Node%d on new Term\n", n.currentTerm, n.nodeID, sourceNodeID)
	}
}

// enter candidate state
func (n *node) enterCandidateState() {
	n.nodeState = Candidate
	n.currentLeader = -1
	n.setTerm(n.currentTerm + 1)

	// vote for self first
	n.votedFor = n.nodeID
	n.votes = make(map[int]bool, n.clusterSize)
	n.votes[n.nodeID] = true

	util.WriteInfo("T%d: \u270b Node%d starts election\n", n.currentTerm, n.nodeID)
}

// start an election, caller should acquire write lock
func (n *node) startElection() {
	n.enterCandidateState()

	// create RV request
	req := &RequestVoteRequest{
		Term:         n.currentTerm,
		CandidateID:  n.nodeID,
		LastLogIndex: n.logMgr.LastIndex(),
		LastLogTerm:  n.logMgr.LastTerm(),
	}

	// request vote (on different goroutines), response will be processed there
	n.peerMgr.BroadcastRequestVote(req, func(reply *RequestVoteReply) { n.handleRequestVoteReply(reply) })

	n.refreshTimer()
}

// callback to be invoked when reply is received (on different goroutine so we need to acquire lock)
func (n *node) handleRequestVoteReply(reply *RequestVoteReply) {
	n.mu.Lock()
	defer n.mu.Unlock()

	if n.tryFollowNewTerm(reply.NodeID, reply.Term, false) {
		// there is a higher term, no need to continue
		return
	}

	if reply.VotedTerm != n.currentTerm || n.nodeState != Candidate || !reply.VoteGranted {
		// stale vote or denied, ignore
		return
	}

	// record and count votes
	n.votes[reply.NodeID] = true
	if n.wonElection() {
		n.enterLeaderState()
		n.sendHeartbeat()
	}
}

// count votes for current node and term and return true if we won
func (n *node) wonElection() bool {
	total := 0
	for _, v := range n.votes {
		if v {
			total++
		}
	}
	return total > n.clusterSize/2
}

// setTerm sets a new term
func (n *node) setTerm(newTerm int) {
	if newTerm < n.currentTerm {
		util.Panicf("can't set new term %d, which is less than current term %d\n", newTerm, n.currentTerm)
	}

	if newTerm > n.currentTerm {
		// reset vote on higher term
		n.votedFor = -1
	}

	n.currentTerm = newTerm
}
