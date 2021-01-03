package kvstore

import (
	"context"
	"io"
	"log"
	"net"
	"sync"
	"time"

	"google.golang.org/grpc"

	"github.com/sidecus/raft/pkg/kvstore/pb"
	"github.com/sidecus/raft/pkg/raft"
)

// RPCServer is used to implement pb.KVStoreRPCServer
type RPCServer struct {
	wg     sync.WaitGroup
	node   raft.INode
	server *grpc.Server
	pb.UnimplementedKVStoreRaftServer
}

// NewServer creates a new RPC server
func NewServer(node raft.INode) *RPCServer {
	return &RPCServer{
		node: node,
	}
}

// AppendEntries implements KVStoreRafterServer.AppendEntries
func (s *RPCServer) AppendEntries(ctx context.Context, req *pb.AppendEntriesRequest) (*pb.AppendEntriesReply, error) {
	ae := toRaftAERequest(req)
	resp, err := s.node.AppendEntries(ctx, ae)

	if err != nil {
		return nil, err
	}

	return fromRaftAEReply(resp), nil
}

// RequestVote requests a vote from the node
func (s *RPCServer) RequestVote(ctx context.Context, req *pb.RequestVoteRequest) (*pb.RequestVoteReply, error) {
	rv := toRaftRVRequest(req)
	resp, err := s.node.RequestVote(ctx, rv)

	if err != nil {
		return nil, err
	}

	return fromRaftRVReply(resp), nil
}

// InstallSnapshot installs snapshot on current node
// TODO[sidecus]: This keeps the reading loop out of raft and it has no idea of chunk reception (and hence no response on each chunk).
// If the snapshot is big it might cause resending from leader
func (s *RPCServer) InstallSnapshot(stream pb.KVStoreRaft_InstallSnapshotServer) error {
	reader, err := raft.NewSnapshotStreamReader(func() (*raft.SnapshotRequest, []byte, error) {
		var pbReq *pb.SnapshotRequest
		var err error
		if pbReq, err = stream.Recv(); err != nil {
			return nil, nil, err
		}

		return toRaftSnapshotRequest(pbReq), pbReq.Data, nil
	})

	if err != nil {
		return err
	}

	// Open snapshot file
	req := reader.RequestHeader()
	file, w, err := raft.CreateSnapshot(s.node.NodeID(), req.SnapshotTerm, req.SnapshotIndex, "remote")
	if err != nil {
		return err
	}
	defer w.Close()

	// Copy to the file
	req.File = file
	if _, err = io.Copy(w, reader); err != nil {
		return err
	}

	// Close snapshot file and try to install
	w.Close()
	var reply *raft.AppendEntriesReply
	ctx, cancel := context.WithTimeout(context.Background(), time.Duration(500)*time.Millisecond)
	defer cancel()
	if reply, err = s.node.InstallSnapshot(ctx, req); err != nil {
		return err
	}

	return stream.SendAndClose(fromRaftAEReply(reply))
}

// Set sets a value in the kv store
func (s *RPCServer) Set(ctx context.Context, req *pb.SetRequest) (*pb.SetReply, error) {
	cmd := toRaftSetRequest(req)

	exeCtx, cancel := context.WithCancel(ctx)
	defer cancel()
	resp, err := s.node.Execute(exeCtx, cmd)

	if err != nil {
		return nil, err
	}

	return fromRaftSetReply(resp), nil
}

// Delete deletes a value from the kv store
func (s *RPCServer) Delete(ctx context.Context, req *pb.DeleteRequest) (*pb.DeleteReply, error) {
	cmd := toRaftDeleteRequest(req)

	exeCtx, cancel := context.WithCancel(ctx)
	defer cancel()
	resp, err := s.node.Execute(exeCtx, cmd)

	if err != nil {
		return nil, err
	}

	return fromRaftDeleteReply(resp), nil
}

// Get implements pb.KVStoreRaftRPCServer.Get
func (s *RPCServer) Get(ctx context.Context, req *pb.GetRequest) (*pb.GetReply, error) {
	gr := toRaftGetRequest(req)
	resp, err := s.node.Get(ctx, gr)

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
