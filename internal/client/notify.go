package client

import (
	"context"
	"fmt"
	"net/http"
	"net/url"
	"time"

	"google.golang.org/protobuf/encoding/protojson"

	pb "github.com/footprintai/containarium/pkg/pb/containarium/v1"
)

// NotifyService client, both transports (#2185).

// notifyTimeout covers TestNotifyChannel, which waits for the ntfy server.
const notifyTimeout = 30 * time.Second

// NotifyChannelPath is the REST path of the NotifyService channel.
const NotifyChannelPath = "/v1/notify/channel"

// NotifyAPI is the transport-independent view of NotifyService.
type NotifyAPI interface {
	SetNotifyChannel(req *pb.SetNotifyChannelRequest) (*pb.SetNotifyChannelResponse, error)
	GetNotifyChannel(req *pb.GetNotifyChannelRequest) (*pb.GetNotifyChannelResponse, error)
	DeleteNotifyChannel(req *pb.DeleteNotifyChannelRequest) (*pb.DeleteNotifyChannelResponse, error)
	TestNotifyChannel(req *pb.TestNotifyChannelRequest) (*pb.TestNotifyChannelResponse, error)
	Close() error
}

// ---------- gRPC ----------

func (c *GRPCClient) notify() pb.NotifyServiceClient {
	return pb.NewNotifyServiceClient(c.conn)
}

func (c *GRPCClient) SetNotifyChannel(req *pb.SetNotifyChannelRequest) (*pb.SetNotifyChannelResponse, error) {
	ctx, cancel := context.WithTimeout(context.Background(), notifyTimeout)
	defer cancel()
	return c.notify().SetNotifyChannel(ctx, req)
}

func (c *GRPCClient) GetNotifyChannel(req *pb.GetNotifyChannelRequest) (*pb.GetNotifyChannelResponse, error) {
	ctx, cancel := context.WithTimeout(context.Background(), notifyTimeout)
	defer cancel()
	return c.notify().GetNotifyChannel(ctx, req)
}

func (c *GRPCClient) DeleteNotifyChannel(req *pb.DeleteNotifyChannelRequest) (*pb.DeleteNotifyChannelResponse, error) {
	ctx, cancel := context.WithTimeout(context.Background(), notifyTimeout)
	defer cancel()
	return c.notify().DeleteNotifyChannel(ctx, req)
}

