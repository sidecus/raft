package raft

import (
	"io"
	"testing"
)

type testStateMachine struct {
	lastApplied int
}

func (sm *testStateMachine) Apply(cmd StateMachineCmd) {
	data := cmd.Data.(int)
	sm.lastApplied = data
}

func (sm *testStateMachine) Get(param ...interface{}) (result interface{}, err error) {
	return param[0], nil
}

func (sm *testStateMachine) Serialize(io.Writer) error {
	return nil
}

func (sm *testStateMachine) Deserialize(reader io.Reader) error {
	return nil
}

func TestNewLogManager(t *testing.T) {
	lm := NewLogMgr(100, &testStateMachine{}, "abcdefg").(*LogManager)

	if lm.nodeID != 100 {
		t.Error("LogManager created with invalid node ID")
	}

	if lm.lastIndex != -1 {
		t.Error("LogManager created with invalid lastIndex")
	}

	if lm.lastTerm != -1 {
		t.Error("LogManager created with invalid lastTerm")
	}

	if lm.commitIndex != -1 {
		t.Error("LogManager created with invalid commitIndex")
	}

	if lm.snapshotIndex != -1 {
		t.Error("LogManager created with invalid snapshotIndex")
	}

	if lm.snapshotTerm != -1 {
		t.Error("LogManager created with invalid snapshotTerm")
	}

	if lm.lastApplied != -1 {
		t.Error("LogManager created with invalid lastApplied")
	}

	if lm.snapshotPath != "abcdefg" {
		t.Error("LogManager created with invalid snapshotPath")
	}
}

func TestProcessCmd(t *testing.T) {
	lm := NewLogMgr(100, &testStateMachine{}, "").(*LogManager)
	cmd := StateMachineCmd{}
	if lm.LastIndex() != -1 {
		t.Error("LastIndex is not -1 upon init")
	}

	if lm.CommitIndex() != -1 {
		t.Error("CommitIndex is not -1 upon init")
	}

	if lm.lastApplied != -1 {
		t.Error("LastApplied is not -1 upon init")
	}

	lm.ProcessCmd(cmd, 3)
	lm.ProcessCmd(cmd, 3)
	lm.ProcessCmd(cmd, 3)
	if lm.LastIndex() != 2 {
		t.Error("LastIndex is incorrect")
	}
	if len(lm.logs) != 3 {
		t.Error("append failed")
	}
	for i, v := range lm.logs {
		if v.Index != i {
			t.Fatal("appended log entry doesn't have correct index")
		}
		if v.Term != 3 {
			t.Fatal("appended log entry doesn't have correct term")
		}
	}

	start := lm.LastIndex()
	end := start + 20
	for i := start; i < end; i++ {
		lm.ProcessCmd(cmd, 4)
	}
	if lm.LastIndex() != end {
		t.Error("LastIndex is incorrect")
	}
	newlogs := lm.logs[start+1 : end+1]
	for i, v := range newlogs {
		if v.Index != start+1+i {
			t.Fatal("appended log entry doesn't have correct index")
		}
		if v.Term != 4 {
			t.Fatal("appended log entry doesn't have correct term")
		}
	}
}

func TestProcessLogs(t *testing.T) {
	sm := &testStateMachine{lastApplied: -1}
	lm := NewLogMgr(100, sm, "").(*LogManager)
	lm.logs = make([]LogEntry, 5)
	lm.lastIndex = 4
	lm.lastTerm = 3
	lm.logs[0] = LogEntry{Index: 0, Term: 1}
	lm.logs[1] = LogEntry{Index: 1, Term: 1}
	lm.logs[2] = LogEntry{Index: 2, Term: 2}
	lm.logs[3] = LogEntry{Index: 3, Term: 2}
	lm.logs[4] = LogEntry{Index: 4, Term: 3}

	// empty entries
	if lm.ProcessLogs(6, 5, make([]LogEntry, 0)) {
		t.Error("ProcessLogs should return false on nonmatching prevIndex/prevTerm")
	}
	if lm.LastIndex() != 4 {
		t.Error("ProcessLogs should not modify lastIndex on nonmatching prev entry")
	}

	if !lm.ProcessLogs(4, 3, make([]LogEntry, 0)) {
		t.Error("ProcessLogs should return true on matching prevIndex/prevTerm")
	}
	if lm.LastIndex() != 4 {
		t.Error("ProcessLogs should not modify lastIndex on empty entries")
	}

	// entries are much newer than logs we have
	entries := generateTestEntries(5, 3)
	if lm.ProcessLogs(5, 3, entries) {
		t.Error("ProcessLogs should return false on nonmatching prevIndex/prevTerm when entries is non empty")
	}
	if lm.LastIndex() != 4 {
		t.Error("ProcessLogs should not modify logs for much newer logs")
	}

	// simple append
	entries = generateTestEntries(4, 10)
	if !lm.ProcessLogs(4, 3, entries) {
		t.Error("ProcessLogs should return true on correct new logs")
	}
	if lm.LastIndex() != 6 || lm.lastTerm != 10 {
		t.Error("ProcessLogs should append correct new logs")
	}

	// 1 overlapping bad entry
	entries = generateTestEntries(3, 10)
	if !lm.ProcessLogs(3, 2, entries) {
		t.Error("ProcessLogs should return true by skiping non matching entries")
	}
	if lm.LastIndex() != 5 || lm.lastTerm != 10 {
		t.Error("ProcessLogs skip bad entries and append rest good ones")
	}

	// all entries are overlapping and non matching
	entries = generateTestEntries(2, 10)
	if !lm.ProcessLogs(2, 2, entries) {
		t.Error("appendLogs should return true by skiping non matching entries")
	}
	if lm.LastIndex() != 4 || lm.lastTerm != 10 || len(lm.logs) != lm.LastIndex()+1 {
		t.Error("appendLogs should append all new good entries")
	}

	// empty logs scenario
	lm.lastIndex = -1
	lm.lastTerm = -1
	entries = generateTestEntries(-1, 10)
	if !lm.ProcessLogs(-1, -1, entries) {
		t.Error("appendLogs should append new entries when it's empty")
	}
	if lm.LastIndex() != 1 || lm.lastTerm != 10 || len(lm.logs) != lm.LastIndex()+1 {
		t.Error("appendLogs should append all new good entries when it's empty")
	}
}

func TestCommit(t *testing.T) {
	sm := &testStateMachine{lastApplied: -1}
	lm := NewLogMgr(100, sm, "").(*LogManager)

	// append two logs to it
	entries := generateTestEntries(-1, 1)
	lm.ProcessLogs(-1, -1, entries)

	// try commit to a much larger index
	ret := lm.Commit(3)
	if !ret {
		t.Error("commit to larger index should commit to last log entry correctly")
	}
	if lm.CommitIndex() != lm.LastIndex() {
		t.Error("commit should update commitIndex correctly")
	}
	if lm.lastApplied != lm.LastIndex() || lm.lastApplied != sm.lastApplied {
		t.Error("commit should apply entries to state machine as appropriate")
	}

	// commit again does nothing
	ret = lm.Commit(5)
	if ret {
		t.Error("commit should be idempotent, and return false on second try")
	}

	if lm.CommitIndex() != 1 || lm.lastApplied != 1 || lm.lastApplied != sm.lastApplied {
		t.Error("noop commit not change anything")
	}
}

func TestHasMatchingPrevEntry(t *testing.T) {
	lm := LogManager{
		logs:          make([]LogEntry, 100),
		lastIndex:     10,
		snapshotIndex: -1,
	}
	lm.logs[9].Term = 4
	lm.logs[10].Term = 5

	if !lm.hasMatchingPrevEntry(-1, -1) {
		t.Error("hasMatchingPrevEntry should return true on -1, -1")
	}

	if lm.hasMatchingPrevEntry(11, 5) {
		t.Error("hasMatchingPrevEntry should return false when prevLogIndex is larger than lastIndex")
	}

	if lm.hasMatchingPrevEntry(9, 5) {
		t.Error("hasMatchingPrevEntry should return false when entry doesn't match")
	}

	if !lm.hasMatchingPrevEntry(10, 5) {
		t.Error("hasMatchingPrevEntry should return true when prev entry matches")
	}
}

func TestAppendLogs(t *testing.T) {
	lm := &LogManager{
		logs:      make([]LogEntry, 5),
		lastIndex: 4,
		lastTerm:  3,
	}
	lm.logs[4] = LogEntry{Index: 4, Term: 3}

	entries := make([]LogEntry, 0)
	lm.appendLogs(entries)
	if lm.LastIndex() != 4 || lm.lastTerm != 3 {
		t.Error("append doesn't update lastIndex/lastTerm correctly on empty input")
	}

	// Append two more entries @term 20
	entries = generateTestEntries(4, 20)
	lm.appendLogs(entries)
	if lm.LastIndex() != 6 || lm.lastTerm != 20 || len(lm.logs) != 7 {
		t.Error("append doesn't update lastIndex/lastTerm correctly on non empty input")
	}

	// null appending with null initial
	lm.logs = []LogEntry{}
	lm.appendLogs([]LogEntry{})
	if lm.lastIndex != -1 || lm.lastTerm != -1 {
		t.Error("appendLogs should set lastIndex/lastTerm to -1")
	}
}

func TestFindFirstConflictingEntryIndex(t *testing.T) {
	lm := &LogManager{
		logs:          make([]LogEntry, 5),
		snapshotIndex: -1,
		lastIndex:     4,
	}
	lm.logs[0] = LogEntry{Index: 0, Term: 1}
	lm.logs[1] = LogEntry{Index: 1, Term: 2}
	lm.logs[2] = LogEntry{Index: 2, Term: 3}
	lm.logs[3] = LogEntry{Index: 3, Term: 4}
	lm.logs[4] = LogEntry{Index: 4, Term: 5}

	// no conflict and all are new entries
	e := generateTestEntries(4, 5)
	ret := lm.findFirstConflictIndex(4, e)
	if ret != e[0].Index || ret != lm.lastIndex+1 {
		t.Error("findFirstConflictingEntryIndex wrong index returned when all incoming data are new and no conflict")
	}

	// one conflicting entries
	e = generateTestEntries(3, 6)
	ret = lm.findFirstConflictIndex(3, e)
	if ret != 4 {
		t.Error("findFirstConflictingEntryIndex returns wrong index when there is one conflicting entry")
	}

	// all entries conflict
	e = generateTestEntries(2, 6)
	ret = lm.findFirstConflictIndex(2, e)
	if ret != e[0].Index {
		t.Error("findFirstConflictingEntryIndex returns wrong index when all entries conflict")
	}

	// all match (duplicate), should return lm.lastIndex + 1
	e = lm.logs[3:]
	ret = lm.findFirstConflictIndex(2, e)
	if ret != lm.lastIndex+1 {
		t.Error("findFirstConflictingEntryIndex returns wrong index when all entries match")
	}

	// empty entries with matching prev index
	e = []LogEntry{}
	ret = lm.findFirstConflictIndex(3, e)
	if ret != 4 {
		t.Error("findFirstConflictingEntryIndex returns wrong index upon heartbeat")
	}

	// empty entries with non matching prev index (-1)
	e = []LogEntry{}
	ret = lm.findFirstConflictIndex(-1, e)
	if ret != 0 {
		t.Error("findFirstConflictingEntryIndex returns wrong index upon heartbeat")
	}
}

func generateTestEntries(prevIndex, newTerm int) []LogEntry {
	num := 2
	entries := make([]LogEntry, num)

	for i := 0; i < num; i++ {
		entries[i] = LogEntry{Index: prevIndex + 1 + i, Term: newTerm}
		entries[i].Cmd.Data = prevIndex + 1 + i
	}

	return entries
}

func TestGetLogEntries(t *testing.T) {
	lm := &LogManager{
		lastIndex: -1,
		lastTerm:  -1,
		// TODO[sidecus]: test different snapshot index scenarios
		snapshotIndex: -1,
	}

	// no elements
	entries, prevIndex, prevTerm := lm.GetLogEntries(0, 1)
	if len(entries) != 0 {
		t.Error("Retrieving from empty logs should return empty")
	}
	if prevIndex != -1 || prevTerm != -1 {
		t.Error("GetLogEntries returns incorrect prev index and term when logs is empty")
	}

	entries, prevIndex, prevTerm = lm.GetLogEntries(0, 0)
	if len(entries) != 0 {
		t.Error("Retrieving from empty logs should return empty")
	}
	if prevIndex != -1 || prevTerm != -1 {
		t.Error("GetLogEntries returns incorrect prev index and term when logs is empty")
	}

	// with 1 element available in logs
	lm.appendLogs([]LogEntry{{Index: 0, Term: 10}})

	entries, prevIndex, prevTerm = lm.GetLogEntries(0, 1)
	if len(entries) != 1 {
		t.Error("should return 1 element")
	}
	if prevIndex != -1 || prevTerm != -1 {
		t.Error("GetLogEntries returns incorrect prev index and term when retrieving the first element")
	}

	entries, prevIndex, prevTerm = lm.GetLogEntries(0, 100)
	if len(entries) != 1 {
		t.Error("should return 1 element")
	}
	if prevIndex != -1 || prevTerm != -1 {
		t.Error("GetLogEntries returns incorrect prev index and term when retrieving the first element")
	}

	entries, prevIndex, prevTerm = lm.GetLogEntries(1, 100)
	if len(entries) != 0 {
		t.Error("should return empty slice when next is at the end")
	}
	if prevIndex != 0 || prevTerm != 10 {
		t.Error("GetLogEntries returns incorrect prev index and term when nextIndex is lastIndex+1")
	}

	entries, prevIndex, prevTerm = lm.GetLogEntries(1, 0)
	if len(entries) != 0 {
		t.Error("Should return empty when count is zero")
	}
	if prevIndex != 0 || prevTerm != 10 {
		t.Error("GetLogEntries returns incorrect prev index and term when logs is empty")
	}
}
