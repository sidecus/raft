package kvstore

import (
	"context"
	"errors"
	"fmt"
	"io"
	"time"

	"github.com/sidecus/raft/pkg/kvstore/pb"
	"github.com/sidecus/raft/pkg/raft"
	"github.com/sidecus/raft/pkg/util"
	"google.golang.org/grpc"
)

const rpcTimeOut = time.Duration(200) * time.Millisecond
const snapshotRPCTimeout = rpcTimeOut * 3

// We need to fine tune below value. If server is busy, a short timeout will cause unnecessary excessive rejection.
const proxyRPCTimeout = rpcTimeOut * 10

var errorInvalidGetRequest = errors.New("Get request doesn't have key")
var errorInvalidExecuteRequest = errors.New("Execute request is neither Set nor Delete")

// KVPeerClient defines the proxy used by kv store, implementing IPeerProxyFactory and IPeerProxy
type KVPeerClient struct {
	raft.NodeInfo
	client pb.KVStoreRaftClient
}

// KVPeerClientFactory is the const factory instance
var KVPeerClientFactory = &KVPeerClient{}

// NewPeerProxy factory method to create a new proxy
func (proxy *KVPeerClient) NewPeerProxy(info raft.NodeInfo) raft.IPeerProxy {
	conn, err := grpc.Dial(info.Endpoint, grpc.WithInsecure())
	if err != nil {
		// Our RPC connection is nonblocking so should not be expecting an error here
		util.Panicln(err)
	}

	client := pb.NewKVStoreRaftClient(conn)

	return &KVPeerClient{
		NodeInfo: raft.NodeInfo{
			NodeID:   info.NodeID,
			Endpoint: info.Endpoint,
		},
		client: client,
	}
}

// AppendEntries sends AE request to one single node
func (proxy *KVPeerClient) AppendEntries(req *raft.AppendEntriesRequest) (reply *raft.AppendEntriesReply, err error) {
	ctx, cancel := context.WithTimeout(context.Background(), rpcTimeOut)
	defer cancel()

	var resp *pb.AppendEntriesReply
	if resp, err = proxy.client.AppendEntries(ctx, fromRaftAERequest(req)); err == nil {
		reply = toRaftAEReply(resp)
	}

	return reply, err
}

// RequestVote handles raft RPC RV calls to a given node
func (proxy *KVPeerClient) RequestVote(req *raft.RequestVoteRequest) (reply *raft.RequestVoteReply, err error) {
	ctx, cancel := context.WithTimeout(context.Background(), rpcTimeOut)
	defer cancel()

	var resp *pb.RequestVoteReply
	rv := fromRaftRVRequest(req)
	if resp, err = proxy.client.RequestVote(ctx, rv); err == nil {
		reply = toRaftRVReply(resp)
	}

	return reply, err
}

// InstallSnapshot takes snapshot request (with snapshotfile) and send it to the remote peer
// onReply is gauranteed to be called
func (proxy *KVPeerClient) InstallSnapshot(req *raft.SnapshotRequest) (reply *raft.AppendEntriesReply, err error) {
	ctx, cancel := context.WithTimeout(context.Background(), snapshotRPCTimeout)
	defer cancel()

	stream, err := proxy.client.InstallSnapshot(ctx)
	if err != nil {
		return nil, err
	}

	reader, err := raft.ReadSnapshot(req.File)
	if err != nil {
		return nil, err
	}

	defer reader.Close()
	writer := newGRPCSnapshotStreamWriter(req, stream)
	if _, err = io.Copy(writer, reader); err != nil {
		return nil, err
	}

	resp, err := stream.CloseAndRecv()
	if err != nil {
		return nil, err
	}

	reply = toRaftAEReply(resp)
	return reply, nil
}

// Get gets values from state machine against leader
func (proxy *KVPeerClient) Get(req *raft.GetRequest) (*raft.GetReply, error) {
	if len(req.Params) != 1 {
		return nil, errorInvalidGetRequest
	}

	ctx, cancel := context.WithTimeout(context.Background(), rpcTimeOut)
	defer cancel()

	gr := fromRaftGetRequest(req)
	resp, err := proxy.client.Get(ctx, gr)

	if err != nil {
		return nil, err
	}

	return toRaftGetReply(resp), nil
}

// Execute runs a command via the leader
func (proxy *KVPeerClient) Execute(cmd *raft.StateMachineCmd) (*raft.ExecuteReply, error) {
	executeMap := make(map[int]func(*raft.StateMachineCmd) (*raft.ExecuteReply, error), 2)
	executeMap[KVCmdSet] = proxy.executeSet
	executeMap[KVCmdDel] = proxy.executeDelete

	handler, ok := executeMap[cmd.CmdType]
	if !ok {
		return nil, errorInvalidExecuteRequest
	}

	return handler(cmd)
}

func (proxy *KVPeerClient) executeSet(cmd *raft.StateMachineCmd) (*raft.ExecuteReply, error) {
	if cmd.CmdType != KVCmdSet {
		util.Panicln("Wrong cmd passed to executeSet")
	}

	req := fromRaftSetRequest(cmd)

	ctx, cancel := context.WithTimeout(context.Background(), proxyRPCTimeout)
	defer cancel()

	var resp *pb.SetReply
	var err error
	if resp, err = proxy.client.Set(ctx, req); err != nil {
		return nil, fmt.Errorf("Error proxying Set request to leader. %s", err)
	}

	return toRaftSetReply(resp), nil
}

func (proxy *KVPeerClient) executeDelete(cmd *raft.StateMachineCmd) (*raft.ExecuteReply, error) {
	if cmd.CmdType != KVCmdDel {
		util.Panicln("Wrong cmd passed to executeDelete")
	}

	req := fromRaftDeleteRequest(cmd)

	ctx, cancel := context.WithTimeout(context.Background(), proxyRPCTimeout)
	defer cancel()

	var resp *pb.DeleteReply
	var err error
	if resp, err = proxy.client.Delete(ctx, req); err != nil {
		return nil, fmt.Errorf("Error proxying Del request to leader. %s", err)
	}

	return toRaftDeleteReply(resp), nil
}
