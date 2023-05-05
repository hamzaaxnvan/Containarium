package cmd

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/footprintai/containarium/internal/client"
	pb "github.com/footprintai/containarium/pkg/pb/containarium/v1"
)

type fakeNotifyClient struct {
	set *pb.SetNotifyChannelRequest
}

func (f *fakeNotifyClient) SetNotifyChannel(req *pb.SetNotifyChannelRequest) (*pb.SetNotifyChannelResponse, error) {
	f.set = req
	return &pb.SetNotifyChannelResponse{Channel: &pb.NotifyChannel{
		Username: "alice", Server: req.GetServer(), Topic: req.GetTopic(), HasToken: req.GetToken() != "", RedactCommand: req.GetRedactCommand(),
	}}, nil
}

func (f *fakeNotifyClient) GetNotifyChannel(*pb.GetNotifyChannelRequest) (*pb.GetNotifyChannelResponse, error) {
	return &pb.GetNotifyChannelResponse{Channel: &pb.NotifyChannel{Username: "alice", Server: "https://n.example.com", Topic: "t", HasToken: true}}, nil
}

func (f *fakeNotifyClient) DeleteNotifyChannel(*pb.DeleteNotifyChannelRequest) (*pb.DeleteNotifyChannelResponse, error) {
	return &pb.DeleteNotifyChannelResponse{}, nil
}
