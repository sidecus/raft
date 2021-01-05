package raft

import (
	"os"
	"testing"
)

func TestNewNode(t *testing.T) {
	tempDir := os.TempDir()
	if err := os.Chdir(tempDir); err != nil {
		t.Fatal("Changing working directory to temp dir failed. Test case cannot continue")
	}

	peerCount := 2
	nodeID := peerCount // last node
	peers := createTestPeerInfo(peerCount)
	ret, err := NewNode(nodeID, peers, &testStateMachine{}, &MockPeerFactory{})
	if err != nil {
		t.Error(err)
	}
	n := ret.(*node)

	if n.nodeID != nodeID {
		t.Error("Node created with invalid node ID")
	}

	if n.clusterSize != peerCount+1 {
		t.Error("Node created with invalid clustersize")
	}

	if n.nodeState != NodeStateFollower {
		t.Error("Node created with invalid starting state")
	}

	if n.currentTerm != 0 {
		t.Error("Node created with invalid starting term")
	}

	if n.currentLeader != -1 {
		t.Error("Node created with invalid current leader")
	}

	if n.votedFor != -1 {
		t.Error("Node created with invalid votedFor")
	}

	if len(n.peerMgr.(*peerManager).peers) != peerCount {
		t.Error("Node created with invalid number of followers")
	}

	if len(n.votes) != 0 {
		t.Error("Node created with invalid votes map")
	}

	if snapshotPath == "" {
		t.Error("NewNode doesn't set snapshot path")
	}
}

func TestNodeSetTerm(t *testing.T) {
	n := &node{
		currentTerm: 0,
		votedFor:    2,
	}

	n.setTerm(1)
	if n.votedFor != -1 {
		t.Error("Set new term doesn't reset votedFor")
	}

	n.votedFor = 2
	n.setTerm(1)
	if n.votedFor != 2 {
		t.Error("Set same term resets votedFor")
	}
}

func TestEnterFollowerState(t *testing.T) {
	timer := &fakeRaftTimer{}
	n := &node{
		nodeState:     NodeStateLeader,
		currentTerm:   0,
		currentLeader: 0,
		votedFor:      0,
		timer:         timer,
	}

	n.enterFollowerState(1, 1)

	if n.nodeState != NodeStateFollower {
		t.Error("enterFollowerState didn't update nodeState to Follower")
	}
	if n.currentLeader != 1 {
		t.Error("enterFollowerState didn't update currentLeader correctly")
	}
	if n.currentTerm != 1 {
		t.Error("enterFollowerState didn't update currentTerm correctly")
	}
	if n.votedFor != -1 {
		t.Error("enterFollowerState didn't reset votedFor on new term")
	}
	if timer.state != NodeStateFollower || timer.term != 1 {
		t.Error("enterFollowerrState didn't reset timer")
	}
}

func TestEnterCandidateState(t *testing.T) {
	timer := &fakeRaftTimer{}
	n := &node{
		nodeID:        100,
		nodeState:     NodeStateLeader,
		currentTerm:   0,
		currentLeader: 0,
		votedFor:      0,
		timer:         timer,
	}

	n.enterCandidateState()

	if n.nodeState != NodeStateCandidate {
		t.Error("enterCandidateState didn't update nodeState to Candidate")
	}
	if n.currentLeader != -1 {
		t.Error("enterCandidateState didn't reset currentLeader to -1")
	}
	if n.currentTerm != 1 {
		t.Error("enterCandidateState didn't increase current term")
	}
	if n.votedFor != 100 {
		t.Error("enterCandidateState didn't vote for self")
	}
	if len(n.votes) != 1 || !n.votes[100] {
		t.Error("enterCandidateState didn't reset other votes")
	}
	if timer.state != NodeStateCandidate || timer.term != n.currentTerm {
		t.Error("enterCandidateState didn't reset timer")
	}
}

func TestEnterLeaderState(t *testing.T) {
	timer := &fakeRaftTimer{}
	n := &node{
		nodeID:        100,
		nodeState:     NodeStateCandidate,
		currentTerm:   50,
		currentLeader: -1,
		peerMgr:       createTestPeerManager(2),
		timer:         timer,
		logMgr: &logManager{
			lastIndex: 3,
		},
	}

	follower0 := n.peerMgr.getPeer(0)
	follower0.nextIndex = 30
	follower0.matchIndex = 20
	follower1 := n.peerMgr.getPeer(1)
	follower1.nextIndex = 100
	follower1.matchIndex = 70

	n.enterLeaderState()

	if n.nodeState != NodeStateLeader {
		t.Error("enterLeaderState didn't update nodeState to Leader")
	}
	if n.currentLeader != 100 {
		t.Error("enterLeaderState didn't set currentLeader to self")
	}
	if n.currentTerm != 50 {
		t.Error("enterLeaderState changes term by mistake")
	}
	if follower0.nextIndex != 4 || follower1.nextIndex != 4 {
		t.Error("enterLeaderState didn't reset nextIndex for peers")
	}
	if follower0.matchIndex != -1 || follower1.matchIndex != -1 {
		t.Error("enterLeaderState didn't reset matchIndex for peers")
	}
	if timer.state != NodeStateLeader || timer.term != 50 {
		t.Error("enterLeaderState didn't reset timer")
	}
}

func TestTryFollowNewTerm(t *testing.T) {
	n := &node{
		nodeID:        0,
		nodeState:     NodeStateLeader,
		currentTerm:   0,
		currentLeader: 0,
		votedFor:      0,
	}
	timer := &fakeRaftTimer{
		state: -1,
	}
	n.timer = timer

	if !n.tryFollowNewTerm(1, 1, false) {
		t.Error("tryFollowNewTerm should return true on new term")
	}
	if n.currentLeader != 1 || n.currentTerm != 1 || n.nodeState != NodeStateFollower {
		t.Error("tryFollowNewTerm doesn't follow upon new term")
	}

	n.nodeState = NodeStateCandidate
	n.currentLeader = 0
	n.currentTerm = 1
	if !n.tryFollowNewTerm(2, 1, true) {
		t.Error("tryFollowNewTerm should return true on AE calls from the same term")
	}
	if n.currentLeader != 2 || n.currentTerm != 1 || n.nodeState != NodeStateFollower {
		t.Error("tryFollowNewTerm should follow AE calls from the same term")
	}
	if timer.state != NodeStateFollower {
		t.Error("tryFollowNewTerm didn't reset timer to follower mode")
	}

	n.nodeState = NodeStateCandidate
	n.currentLeader = 0
	n.currentTerm = 1
	if n.tryFollowNewTerm(1, 1, false) {
		t.Error("tryFollowNewTerm should not return true on same term when it's not AE call")
	}
	if n.currentLeader != 0 || n.currentTerm != 1 || n.nodeState != NodeStateCandidate {
		t.Error("tryFollowNewTerm updates node state incorrectly")
	}
}

func TestOnSnapshotPart(t *testing.T) {
	fakeTimer := &fakeRaftTimer{state: -1}
	n := &node{
		nodeState: NodeStateLeader,
		timer:     fakeTimer,
	}

	part := &SnapshotRequest{}

	// same term
	part.LeaderID = 3
	part.Term = 0
	if !n.OnSnapshotPart(part) {
		t.Error("onSnapshotPart returns false when it should accept the part")
	}
	if n.currentTerm != part.Term || n.currentLeader != part.LeaderID {
		t.Error("onSnapshotPart didn't follow node on same term")
	}
	if fakeTimer.state != NodeStateFollower || fakeTimer.term != part.Term {
		t.Error("onSnapshotPart didn't reset timer")
	}

	// higher term
	n.nodeState = NodeStateLeader
	part.LeaderID = 4
	part.Term = 1
	if !n.OnSnapshotPart(part) {
		t.Error("onSnapshotPart returns false when it should accept the part")
	}
	if n.currentTerm != part.Term || n.currentLeader != part.LeaderID {
		t.Error("onSnapshotPart didn't follow node on same term")
	}
	if fakeTimer.state != NodeStateFollower || fakeTimer.term != part.Term {
		t.Error("onSnapshotPart didn't reset timer")
	}

	// lower term
	n.nodeState = NodeStateLeader
	n.currentLeader = 20
	n.currentTerm = 3
	part.LeaderID = 4
	part.Term = 2
	fakeTimer.state = -1
	if n.OnSnapshotPart(part) {
		t.Error("onSnapshotPart returns true when it should deny the part")
	}
	if n.currentTerm != 3 || n.currentLeader != 20 {
		t.Error("onSnapshotPart should not modify node sate on lower term")
	}
	if fakeTimer.state != -1 {
		t.Error("onSnapshotPart should not reset timer on lower term")
	}
}

func TestLeaderCommit(t *testing.T) {
	sm := &testStateMachine{
		lastApplied: -111,
	}
	peerMgr := createTestPeerManager(2)
	logMgr := newLogMgr(100, sm).(*logManager)

	peerMgr.getPeer(0).nextIndex = 2
	peerMgr.getPeer(0).matchIndex = 1
	peerMgr.getPeer(1).nextIndex = 3
	peerMgr.getPeer(1).matchIndex = 1

	for i := 0; i < 5; i++ {
		logMgr.ProcessCmd(StateMachineCmd{
			CmdType: 1,
			Data:    i * 10,
		}, i+1)
	}

	n := &node{
		clusterSize: 3,
		currentTerm: 5,
		logMgr:      logMgr,
		peerMgr:     peerMgr,
	}

	// We only have a match on 1st entry, but it's of a lower term
	n.leaderCommit()
	if logMgr.commitIndex != -1 {
		t.Error("leaderCommit shall not commit entries from previous term")
	}
	if sm.lastApplied != -111 {
		t.Error("leaderCommit shall not trigger apply of entires from previous term")
	}

	// We only have a majority match on 5th entry in the same term
	expectedCommit := 4
	peerMgr.getPeer(1).matchIndex = expectedCommit
	n.leaderCommit()
	if logMgr.commitIndex != expectedCommit {
		t.Error("leaderCommit shall commit to the right entry")
	}
	if logMgr.lastApplied != expectedCommit || sm.lastApplied != logMgr.logs[expectedCommit].Cmd.Data {
		t.Error("leaderCommit trigger apply to the right entry")
	}
}

func TestReplicateData(t *testing.T) {
	logMgr := newLogMgr(100, &testStateMachine{lastApplied: -111}).(*logManager)
	for i := 0; i < 5; i++ {
		logMgr.ProcessCmd(StateMachineCmd{
			CmdType: 1,
			Data:    i * 10,
		}, i+1)
	}

	peers := make(map[int]NodeInfo, 1)
	peers[1] = NodeInfo{NodeID: 1}
	peerMgr := newPeerManager(peers, nil, &MockPeerFactory{})
	peer1 := peerMgr.getPeer(1)
	proxy1 := peer1.IPeerProxy.(*MockPeerProxy)

	n := &node{
		nodeID:      2,
		nodeState:   NodeStateLeader,
		clusterSize: 3,
		currentTerm: 5,
		logMgr:      logMgr,
		peerMgr:     peerMgr,
	}

	// nextIndex is larger than lastIndex, should send empty request
	peer1.nextIndex = logMgr.lastIndex + 1
	peer1.matchIndex = 1
	n.replicateData(peer1)
	if proxy1.aeReq == nil {
		t.Error("replicateData should replicate even when nextIndex is higher than lastIndex")
	}
	aeReq := proxy1.aeReq
	if aeReq.LeaderID != n.nodeID || aeReq.Term != n.currentTerm ||
		aeReq.PrevLogIndex != peer1.nextIndex-1 || aeReq.PrevLogTerm != logMgr.logs[aeReq.PrevLogIndex].Term {
		t.Error("wrong info are being replicated")
	}
	if len(aeReq.Entries) != 0 {
		t.Error("wrong payload when nextIndex is higher than lastIndex")
	}

	// nextIndex is smaler than lastIndex
	peer1.nextIndex = logMgr.lastIndex - 2
	peer1.matchIndex = 1
	n.replicateData(peer1)
	if proxy1.aeReq == nil {
		t.Error("replicateLogsTo should replicate when nextIndex smaller")
	}
	aeReq = proxy1.aeReq
	if aeReq.LeaderID != n.nodeID || aeReq.Term != n.currentTerm ||
		aeReq.PrevLogIndex != logMgr.lastIndex-3 || aeReq.PrevLogTerm != logMgr.logs[aeReq.PrevLogIndex].Term {
		t.Error("wrong info are being replicated")
	}
	if len(aeReq.Entries) != 3 || aeReq.Entries[0].Index != logMgr.logs[logMgr.lastIndex-2].Index {
		t.Error("replicated entries contain bad entries")
	}

	// nextIndex is the same as snapshotIndex, should trigger snapshot request
	logMgr.snapshotIndex = 3
	logMgr.snapshotTerm = 2
	logMgr.lastSnapshotFile = "snapshot"
	peer1.nextIndex = 3
	n.replicateData(peer1)
	if proxy1.isReq == nil {
		t.Error("replicateLogsTo should replicate snapshot but it didn't (or replicated more than once)")
	}
	isReq := proxy1.isReq
	if isReq.LeaderID != n.nodeID || isReq.Term != n.currentTerm ||
		isReq.File != logMgr.lastSnapshotFile ||
		isReq.SnapshotIndex != logMgr.snapshotIndex || isReq.SnapshotTerm != logMgr.snapshotTerm {
		t.Error("wrong info in SnapshotRequest")
	}

	// nextIndex is smaller than snapshotIndex
	logMgr.snapshotIndex = 5
	logMgr.snapshotTerm = 3
	logMgr.lastSnapshotFile = "snapshotsmaller"
	logMgr.lastIndex = logMgr.snapshotIndex + len(logMgr.logs)
	peer1.nextIndex = 4
	n.replicateData(peer1)
	if proxy1.isReq == nil {
		t.Error("replicateLogsTo should replicate snapshot but it didn't")
	}
	isReq = proxy1.isReq
	if isReq.LeaderID != n.nodeID || isReq.Term != n.currentTerm || isReq.File != logMgr.lastSnapshotFile || isReq.SnapshotIndex != logMgr.snapshotIndex || isReq.SnapshotTerm != logMgr.snapshotTerm {
		t.Error("wrong info in SnapshotRequest")
	}
}
func TestWonElection(t *testing.T) {
	n := &node{}
	n.clusterSize = 3
	n.votes = make(map[int]bool)

	n.votes[0] = true
	if n.wonElection() {
		t.Error("wonElection should return false on 1 vote out of 3")
	}

	n.votes[2] = true
	if !n.wonElection() {
		t.Error("wonElection should return true on 2 votes out of 3")
	}
}
