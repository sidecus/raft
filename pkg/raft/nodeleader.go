package raft

import (
	"context"
	"errors"
	"time"

	"github.com/sidecus/raft/pkg/util"
)

const rpcTimeOut = time.Duration(200) * time.Millisecond
const rpcSnapshotTimeout = rpcTimeOut * 3

var errNoLongerLeader = errors.New("Node is no longer leader")

// enterLeaderState resets leader indicies. Caller should acquire writer lock
func (n *node) enterLeaderState() {
	n.nodeState = NodeStateLeader
	n.currentLeader = n.nodeID

	// reset all follower's indicies
	n.peerMgr.ResetFollowerIndicies(n.logMgr.LastIndex())

	util.WriteInfo("T%d: \U0001f451 Node%d won election\n", n.currentTerm, n.nodeID)
}

// send heartbeat, caller should acquire at least reader lock
func (n *node) sendHeartbeat() {
	for _, p := range n.peerMgr.GetPeers() {
		p.TriggerReplication()
	}

	// 5.2 - refresh timer
	n.refreshTimer()
}

// replicateData replicates data to follower. It replicates snapshot or next batch of logs to the follower.
// If nothing more to replicate, it'll send heartbeat like message with empty entries.
// There are two cases where there is noting to replicate: a. we are still looking for a matching index. b. there is no new info
// This is called in the replication goroutine for each follower
func (n *node) replicateData(followerID int) {
	replicateFunc := n.prepareReplicate(followerID)
	reply, err := replicateFunc()

	if err != nil {
		util.WriteTrace("T%d: Failed to replicate data to Node%d. %s", n.currentTerm, followerID, err)
		return
	}

	n.handleReplicationReply(reply)
}

// prepareReplicate prepares replication for the given node.
// We need reader lock on node
func (n *node) prepareReplicate(followerID int) func() (*AppendEntriesReply, error) {
	n.mu.RLock()
	defer n.mu.RUnlock()

	if n.nodeState != NodeStateLeader {
		return func() (*AppendEntriesReply, error) {
			return nil, errNoLongerLeader
		}
	}

	follower := n.peerMgr.GetPeer(followerID)
	currentTerm := n.currentTerm
	snapshotIndex := n.logMgr.SnapshotIndex()

	// Return a func to send snapshot when needed
	if follower.nextIndex <= snapshotIndex {
		req := n.createSnapshotRequest()
		return func() (*AppendEntriesReply, error) {
			ctx, cancel := context.WithTimeout(context.Background(), rpcSnapshotTimeout)
			defer cancel()
			util.WriteTrace("T%d: Sending snapshot to Node%d (L%d)\n", currentTerm, follower.NodeID, snapshotIndex)
			return follower.InstallSnapshot(ctx, req)
		}
	}

	// Return a func to send logs
	maxEntryCount := maxAppendEntriesCount
	if !follower.HasMatch() {
		maxEntryCount = 0
	}
	req := n.createAERequest(follower.nextIndex, maxEntryCount)
	return func() (*AppendEntriesReply, error) {
		ctx, cancel := context.WithTimeout(context.Background(), rpcTimeOut)
		defer cancel()
		util.WriteVerbose("T%d: Sending replication request to Node%d. prevIndex: %d, prevTerm: %d, entryCnt: %d\n", currentTerm, follower.NodeID, req.PrevLogIndex, req.PrevLogTerm, len(req.Entries))
		return follower.AppendEntries(ctx, req)
	}
}

// handleReplicationReply handles append entries reply for replications.
// Need writer lock
func (n *node) handleReplicationReply(reply *AppendEntriesReply) {
	n.mu.Lock()
	defer n.mu.Unlock()

	// If there is a higher term, follow and stop processing
	if n.tryFollowNewTerm(reply.LeaderID, reply.Term, false) {
		return
	}

	follower := n.peerMgr.GetPeer(reply.NodeID)

	// 5.3 update follower indicies based on reply and last match index info from the reply
	// Then check whether there are logs to commit
	follower.UpdateMatchIndex(reply.Success, reply.LastMatch)
	newCommit := reply.Success && n.leaderCommit()

	// replicate more if there is remaining data, or there is a new commit
	if follower.HasMoreToReplicate(n.logMgr.LastIndex()) || newCommit {
		follower.TriggerReplication()
	}
}

// leaderCommit commits to the last entry with quorum
// This should only be called by leader upon AE reply handling
// Returns true if anything is committed
func (n *node) leaderCommit() bool {
	commitIndex := n.logMgr.CommitIndex()
	for i := n.logMgr.LastIndex(); i > n.logMgr.CommitIndex(); i-- {
		entry := n.logMgr.GetLogEntry(i)

		if entry.Term < n.currentTerm {
			// 5.4.2 Raft doesn't allow committing of previous terms
			// A leader shall only commit entries added by itself, and term is the indication of ownership
			break
		} else if entry.Term > n.currentTerm {
			// This will never happen, adding for safety purpose
			continue
		}

		// If we reach here, we can safely declare sole ownership of the ith entry
		if n.peerMgr.QuorumReached(i) {
			commitIndex = i
			break
		}
	}

	if commitIndex > n.logMgr.CommitIndex() {
		util.WriteTrace("T%d: Leader%d committing to L%d upon quorum", n.currentTerm, n.nodeID, commitIndex)
		n.commitTo(commitIndex)
		return true
	}

	return false
}

// createAERequest creates an AppendEntriesRequest with proper log payload
func (n *node) createAERequest(nextIdx int, maxCnt int) *AppendEntriesRequest {
	// make sure nextIdx is larger than n.logMgr.SnapshotIndex()
	// nextIdx <= n.logMgr.SnapshotIndex() will cause panic on log entry retrieval.
	startIdx := util.Max(nextIdx, n.logMgr.SnapshotIndex()+1)
	endIdx := util.Min(n.logMgr.LastIndex()+1, startIdx+maxCnt)

	entries, prevIdx, prevTerm := n.logMgr.GetLogEntries(startIdx, endIdx)

	req := &AppendEntriesRequest{
		Term:         n.currentTerm,
		LeaderID:     n.nodeID,
		PrevLogIndex: prevIdx,
		PrevLogTerm:  prevTerm,
		Entries:      entries,
		LeaderCommit: n.logMgr.CommitIndex(),
	}

	return req
}

// createSnapshotRequest creates a snapshot request to send to follower
func (n *node) createSnapshotRequest() *SnapshotRequest {
	return &SnapshotRequest{
		Term:          n.currentTerm,
		LeaderID:      n.nodeID,
		SnapshotIndex: n.logMgr.SnapshotIndex(),
		SnapshotTerm:  n.logMgr.SnapshotTerm(),
		File:          n.logMgr.SnapshotFile(),
	}
}
