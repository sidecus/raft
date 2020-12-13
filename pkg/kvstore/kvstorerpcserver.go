package kvstore

import (
	"context"
	"errors"
	"log"
	"net"
	"sync"

	"google.golang.org/grpc"

	"github.com/sidecus/raft/pkg/kvstore/pb"
	"github.com/sidecus/raft/pkg/raft"
)

var errorEmptySnapshot = errors.New("empty snapshot received")

// RPCServer is used to implement pb.KVStoreRPCServer
type RPCServer struct {
	wg     sync.WaitGroup
	node   raft.INodeRPCProvider
	server *grpc.Server
	pb.UnimplementedKVStoreRaftServer
}

// NewServer creates a new RPC server
func NewServer(node raft.INodeRPCProvider) *RPCServer {
	return &RPCServer{
		node: node,
	}
}

// AppendEntries implements KVStoreRafterServer.AppendEntries
func (s *RPCServer) AppendEntries(ctx context.Context, req *pb.AppendEntriesRequest) (*pb.AppendEntriesReply, error) {
	ae := toRaftAERequest(req)
	resp, err := s.node.AppendEntries(ae)

	if err != nil {
		return nil, err
	}

	return fromRaftAEReply(resp), nil
}

// RequestVote requests a vote from the node
func (s *RPCServer) RequestVote(ctx context.Context, req *pb.RequestVoteRequest) (*pb.RequestVoteReply, error) {
	rv := toRaftRVRequest(req)
	resp, err := s.node.RequestVote(rv)

	if err != nil {
		return nil, err
	}

	return fromRaftRVReply(resp), nil
}

// InstallSnapshot installs snapshot on the target node
func (s *RPCServer) InstallSnapshot(stream pb.KVStoreRaft_InstallSnapshotServer) error {
	// Receive snapshot as file first
	req, err := raft.ReceiveSnapshot(s.node.NodeID(), func() (*raft.SnapshotRequest, []byte, error) {
		rpcSnapshotReq, err := stream.Recv()
		if err != nil {
			return nil, nil, err
		}

		return toRaftSnapshotRequest(rpcSnapshotReq), rpcSnapshotReq.Data, nil
	})

	if err != nil {
		return err
	}

	var reply *raft.AppendEntriesReply
	if reply, err = s.node.InstallSnapshot(req); err != nil {
		return err
	}
	return stream.SendAndClose(fromRaftAEReply(reply))
}

// Set sets a value in the kv store
func (s *RPCServer) Set(ctx context.Context, req *pb.SetRequest) (*pb.SetReply, error) {
	cmd := toRaftSetRequest(req)
	resp, err := s.node.Execute(cmd)

	if err != nil {
		return nil, err
	}

	return fromRaftSetReply(resp), nil
}

// Delete deletes a value from the kv store
func (s *RPCServer) Delete(ctx context.Context, req *pb.DeleteRequest) (*pb.DeleteReply, error) {
	cmd := toRaftDeleteRequest(req)
	resp, err := s.node.Execute(cmd)

	if err != nil {
		return nil, err
	}

	return fromRaftDeleteReply(resp), nil
}

// Get implements pb.KVStoreRaftRPCServer.Get
func (s *RPCServer) Get(ctx context.Context, req *pb.GetRequest) (*pb.GetReply, error) {
	gr := toRaftGetRequest(req)
	resp, err := s.node.Get(gr)

	if err != nil {
		return nil, err
	}

	return fromRaftGetReply(resp), nil
}

// Start starts the grpc server on a different go routine
func (s *RPCServer) Start(port string) {
	var opts []grpc.ServerOption
	s.server = grpc.NewServer(opts...)
	pb.RegisterKVStoreRaftServer(s.server, s)

	s.wg.Add(1)
	go func() {
		lis, err := net.Listen("tcp", ":"+port)
		if err != nil {
			log.Fatalf("Cannot listen on port %s. Error:%s", port, err.Error())
		}

		s.server.Serve(lis)
		s.wg.Done()
	}()
}

// Stop stops the rpc server
func (s *RPCServer) Stop() {
	s.server.Stop()
	s.wg.Wait()
}
