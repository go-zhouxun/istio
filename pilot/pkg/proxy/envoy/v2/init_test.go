// Copyright 2018 Istio Authors
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//     http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.
package v2_test

import (
	"context"
	"encoding/binary"
	"errors"
	"fmt"
	"net"
	"time"

	endpoint "github.com/envoyproxy/go-control-plane/envoy/config/endpoint/v3"
	"github.com/golang/protobuf/ptypes"
	structpb "github.com/golang/protobuf/ptypes/struct"

	"istio.io/istio/pilot/pkg/model"

	v2 "istio.io/istio/pilot/pkg/proxy/envoy/v2"

	xdsapi "github.com/envoyproxy/go-control-plane/envoy/api/v2"
	core "github.com/envoyproxy/go-control-plane/envoy/api/v2/core"
	ads "github.com/envoyproxy/go-control-plane/envoy/service/discovery/v2"
	"google.golang.org/genproto/googleapis/rpc/status"
	"google.golang.org/grpc"

	"istio.io/istio/tests/util"
)

var nodeMetadata = &structpb.Struct{Fields: map[string]*structpb.Value{
	"ISTIO_VERSION": {Kind: &structpb.Value_StringValue{StringValue: "1.3"}}, // actual value doesn't matter
}}

// Extract cluster load assignment from a discovery response.
func getLoadAssignment(res1 *xdsapi.DiscoveryResponse) (*endpoint.ClusterLoadAssignment, error) {
	if res1.TypeUrl != v2.EndpointTypeV3 {
		return nil, errors.New("Invalid typeURL" + res1.TypeUrl)
	}
	if res1.Resources[0].TypeUrl != v2.EndpointTypeV3 {
		return nil, errors.New("Invalid resource typeURL" + res1.Resources[0].TypeUrl)
	}
	cla := &endpoint.ClusterLoadAssignment{}
	err := ptypes.UnmarshalAny(res1.Resources[0], cla)
	if err != nil {
		return nil, err
	}
	return cla, nil
}

func testIP(id uint32) string {
	ipb := []byte{0, 0, 0, 0}
	binary.BigEndian.PutUint32(ipb, id)
	return net.IP(ipb).String()
}

func connectADS(url string) (ads.AggregatedDiscoveryService_StreamAggregatedResourcesClient, util.TearDownFunc, error) {
	conn, err := grpc.Dial(url, grpc.WithInsecure(), grpc.WithBlock())
	if err != nil {
		return nil, nil, fmt.Errorf("GRPC dial failed: %s", err)
	}
	xds := ads.NewAggregatedDiscoveryServiceClient(conn)
	client, err := xds.StreamAggregatedResources(context.Background())
	if err != nil {
		return nil, nil, fmt.Errorf("stream resources failed: %s", err)
	}

	return client, func() {
		_ = client.CloseSend()
		_ = conn.Close()
	}, nil
}

func adsReceive(ads ads.AggregatedDiscoveryService_StreamAggregatedResourcesClient, to time.Duration) (*xdsapi.DiscoveryResponse, error) {
	done := make(chan int, 1)
	t := time.NewTimer(to)
	defer func() {
		done <- 1
	}()
	go func() {
		select {
		case <-t.C:
			_ = ads.CloseSend() // will result in adsRecv closing as well, interrupting the blocking recv
		case <-done:
			_ = t.Stop()
		}
	}()
	return ads.Recv()
}

func sendEDSReq(clusters []string, node string, edsstr ads.AggregatedDiscoveryService_StreamAggregatedResourcesClient) error {
	err := edsstr.Send(&xdsapi.DiscoveryRequest{
		ResponseNonce: time.Now().String(),
		Node: &core.Node{
			Id:       node,
			Metadata: nodeMetadata,
		},
		TypeUrl:       v2.EndpointTypeV3,
		ResourceNames: clusters,
	})
	if err != nil {
		return fmt.Errorf("EDS request failed: %s", err)
	}

	return nil
}

func sendEDSNack(_ []string, node string, client ads.AggregatedDiscoveryService_StreamAggregatedResourcesClient) error {
	return sendXds(node, client, v2.EndpointTypeV3, "NOPE!")
}

// If pilot is reset, envoy will connect with a nonce/version info set on the previous
// connection to pilot. In HA case this may be a different pilot. This is a regression test for
// reconnect problems.
func sendEDSReqReconnect(clusters []string, client ads.AggregatedDiscoveryService_StreamAggregatedResourcesClient, res *xdsapi.DiscoveryResponse) error {
	err := client.Send(&xdsapi.DiscoveryRequest{
		Node: &core.Node{
			Id:       sidecarID(app3Ip, "app3"),
			Metadata: nodeMetadata,
		},
		TypeUrl:       v2.EndpointTypeV3,
		ResponseNonce: res.Nonce,
		VersionInfo:   res.VersionInfo,
		ResourceNames: clusters})
	if err != nil {
		return fmt.Errorf("EDS reconnect failed: %s", err)
	}

	return nil
}

func sendLDSReq(node string, client ads.AggregatedDiscoveryService_StreamAggregatedResourcesClient) error {
	return sendXds(node, client, v2.ListenerType, "")
}

func sendLDSReqWithLabels(node string, ldsstr ads.AggregatedDiscoveryService_StreamAggregatedResourcesClient, labels map[string]string) error {
	err := ldsstr.Send(&xdsapi.DiscoveryRequest{
		ResponseNonce: time.Now().String(),
		Node: &core.Node{
			Id:       node,
			Metadata: model.NodeMetadata{Labels: labels}.ToStruct(),
		},
		TypeUrl: v2.ListenerType})
	if err != nil {
		return fmt.Errorf("LDS request failed: %s", err)
	}

	return nil
}

func sendLDSNack(node string, client ads.AggregatedDiscoveryService_StreamAggregatedResourcesClient) error {
	return sendXds(node, client, v2.ListenerType, "NOPE!")
}

func sendRDSReq(node string, routes []string, nonce string, rdsstr ads.AggregatedDiscoveryService_StreamAggregatedResourcesClient) error {
	err := rdsstr.Send(&xdsapi.DiscoveryRequest{
		ResponseNonce: nonce,
		Node: &core.Node{
			Id:       node,
			Metadata: nodeMetadata,
		},
		TypeUrl:       v2.RouteType,
		ResourceNames: routes})
	if err != nil {
		return fmt.Errorf("RDS request failed: %s", err)
	}

	return nil
}

func sendRDSNack(node string, _ []string, nonce string, rdsstr ads.AggregatedDiscoveryService_StreamAggregatedResourcesClient) error {
	err := rdsstr.Send(&xdsapi.DiscoveryRequest{
		ResponseNonce: nonce,
		Node: &core.Node{
			Id:       node,
			Metadata: nodeMetadata,
		},
		TypeUrl:     v2.RouteType,
		ErrorDetail: &status.Status{Message: "NOPE!"}})
	if err != nil {
		return fmt.Errorf("RDS NACK failed: %s", err)
	}

	return nil
}

func sendCDSReq(node string, client ads.AggregatedDiscoveryService_StreamAggregatedResourcesClient) error {
	return sendXds(node, client, v2.ClusterType, "")
}

func sendCDSNack(node string, client ads.AggregatedDiscoveryService_StreamAggregatedResourcesClient) error {
	return sendXds(node, client, v2.ClusterType, "NOPE!")
}

func sendXds(node string, client ads.AggregatedDiscoveryService_StreamAggregatedResourcesClient, typeURL string, errMsg string) error {
	var errorDetail *status.Status
	if errMsg != "" {
		errorDetail = &status.Status{Message: errMsg}
	}
	err := client.Send(&xdsapi.DiscoveryRequest{
		ResponseNonce: time.Now().String(),
		Node: &core.Node{
			Id:       node,
			Metadata: nodeMetadata,
		},
		ErrorDetail: errorDetail,
		TypeUrl:     typeURL})
	if err != nil {
		return fmt.Errorf("%v Request failed: %s", typeURL, err)
	}

	return nil
}
