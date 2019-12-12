// Code generated by protoc-gen-gogo. DO NOT EDIT.
// source: mesh/v1alpha1/network.proto

package v1alpha1

import (
	fmt "fmt"
	proto "github.com/gogo/protobuf/proto"
	io "io"
	_ "istio.io/gogo-genproto/googleapis/google/api"
	math "math"
	math_bits "math/bits"
)

// Reference imports to suppress errors if they are not otherwise used.
var _ = proto.Marshal
var _ = fmt.Errorf
var _ = math.Inf

// This is a compile-time assertion to ensure that this generated file
// is compatible with the proto package it is being compiled against.
// A compilation error at this line likely means your copy of the
// proto package needs to be updated.
const _ = proto.GoGoProtoPackageIsVersion3 // please upgrade the proto package

// Network provides information about the endpoints in a routable L3
// network. A single routable L3 network can have one or more service
// registries. Note that the network has no relation to the locality of the
// endpoint. The endpoint locality will be obtained from the service
// registry.
type Network struct {
	// The list of endpoints in the network (obtained through the
	// constituent service registries or from CIDR ranges). All endpoints in
	// the network are directly accessible to one another.
	Endpoints []*Network_NetworkEndpoints `protobuf:"bytes,2,rep,name=endpoints,proto3" json:"endpoints,omitempty"`
	// Set of gateways associated with the network.
	Gateways             []*Network_IstioNetworkGateway `protobuf:"bytes,3,rep,name=gateways,proto3" json:"gateways,omitempty"`
	XXX_NoUnkeyedLiteral struct{}                       `json:"-"`
	XXX_unrecognized     []byte                         `json:"-"`
	XXX_sizecache        int32                          `json:"-"`
}

func (m *Network) Reset()         { *m = Network{} }
func (m *Network) String() string { return proto.CompactTextString(m) }
func (*Network) ProtoMessage()    {}
func (*Network) Descriptor() ([]byte, []int) {
	return fileDescriptor_a15df2a96e10cd86, []int{0}
}
func (m *Network) XXX_Unmarshal(b []byte) error {
	return m.Unmarshal(b)
}
func (m *Network) XXX_Marshal(b []byte, deterministic bool) ([]byte, error) {
	if deterministic {
		return xxx_messageInfo_Network.Marshal(b, m, deterministic)
	} else {
		b = b[:cap(b)]
		n, err := m.MarshalToSizedBuffer(b)
		if err != nil {
			return nil, err
		}
		return b[:n], nil
	}
}
func (m *Network) XXX_Merge(src proto.Message) {
	xxx_messageInfo_Network.Merge(m, src)
}
func (m *Network) XXX_Size() int {
	return m.Size()
}
func (m *Network) XXX_DiscardUnknown() {
	xxx_messageInfo_Network.DiscardUnknown(m)
}

var xxx_messageInfo_Network proto.InternalMessageInfo

func (m *Network) GetEndpoints() []*Network_NetworkEndpoints {
	if m != nil {
		return m.Endpoints
	}
	return nil
}

func (m *Network) GetGateways() []*Network_IstioNetworkGateway {
	if m != nil {
		return m.Gateways
	}
	return nil
}

// NetworkEndpoints describes how the network associated with an endpoint
// should be inferred. An endpoint will be assigned to a network based on
// the following rules:
//
// 1. Implicitly: If the registry explicitly provides information about
// the network to which the endpoint belongs to. In some cases, its
// possible to indicate the network associated with the endpoint by
// adding the `ISTIO_META_NETWORK` environment variable to the sidecar.
//
// 2. Explicitly:
//
//    a. By matching the registry name with one of the "fromRegistry"
//    in the mesh config. A "from_registry" can only be assigned to a
//    single network.
//
//    b. By matching the IP against one of the CIDR ranges in a mesh
//    config network. The CIDR ranges must not overlap and be assigned to
//    a single network.
//
// (2) will override (1) if both are present.
type Network_NetworkEndpoints struct {
	// Types that are valid to be assigned to Ne:
	//	*Network_NetworkEndpoints_FromCidr
	//	*Network_NetworkEndpoints_FromRegistry
	Ne                   isNetwork_NetworkEndpoints_Ne `protobuf_oneof:"ne"`
	XXX_NoUnkeyedLiteral struct{}                      `json:"-"`
	XXX_unrecognized     []byte                        `json:"-"`
	XXX_sizecache        int32                         `json:"-"`
}

func (m *Network_NetworkEndpoints) Reset()         { *m = Network_NetworkEndpoints{} }
func (m *Network_NetworkEndpoints) String() string { return proto.CompactTextString(m) }
func (*Network_NetworkEndpoints) ProtoMessage()    {}
func (*Network_NetworkEndpoints) Descriptor() ([]byte, []int) {
	return fileDescriptor_a15df2a96e10cd86, []int{0, 0}
}
func (m *Network_NetworkEndpoints) XXX_Unmarshal(b []byte) error {
	return m.Unmarshal(b)
}
func (m *Network_NetworkEndpoints) XXX_Marshal(b []byte, deterministic bool) ([]byte, error) {
	if deterministic {
		return xxx_messageInfo_Network_NetworkEndpoints.Marshal(b, m, deterministic)
	} else {
		b = b[:cap(b)]
		n, err := m.MarshalToSizedBuffer(b)
		if err != nil {
			return nil, err
		}
		return b[:n], nil
	}
}
func (m *Network_NetworkEndpoints) XXX_Merge(src proto.Message) {
	xxx_messageInfo_Network_NetworkEndpoints.Merge(m, src)
}
func (m *Network_NetworkEndpoints) XXX_Size() int {
	return m.Size()
}
func (m *Network_NetworkEndpoints) XXX_DiscardUnknown() {
	xxx_messageInfo_Network_NetworkEndpoints.DiscardUnknown(m)
}

var xxx_messageInfo_Network_NetworkEndpoints proto.InternalMessageInfo

type isNetwork_NetworkEndpoints_Ne interface {
	isNetwork_NetworkEndpoints_Ne()
	MarshalTo([]byte) (int, error)
	Size() int
}

type Network_NetworkEndpoints_FromCidr struct {
	FromCidr string `protobuf:"bytes,1,opt,name=from_cidr,json=fromCidr,proto3,oneof"`
}
type Network_NetworkEndpoints_FromRegistry struct {
	FromRegistry string `protobuf:"bytes,2,opt,name=from_registry,json=fromRegistry,proto3,oneof"`
}

func (*Network_NetworkEndpoints_FromCidr) isNetwork_NetworkEndpoints_Ne()     {}
func (*Network_NetworkEndpoints_FromRegistry) isNetwork_NetworkEndpoints_Ne() {}

func (m *Network_NetworkEndpoints) GetNe() isNetwork_NetworkEndpoints_Ne {
	if m != nil {
		return m.Ne
	}
	return nil
}

func (m *Network_NetworkEndpoints) GetFromCidr() string {
	if x, ok := m.GetNe().(*Network_NetworkEndpoints_FromCidr); ok {
		return x.FromCidr
	}
	return ""
}

func (m *Network_NetworkEndpoints) GetFromRegistry() string {
	if x, ok := m.GetNe().(*Network_NetworkEndpoints_FromRegistry); ok {
		return x.FromRegistry
	}
	return ""
}

// XXX_OneofWrappers is for the internal use of the proto package.
func (*Network_NetworkEndpoints) XXX_OneofWrappers() []interface{} {
	return []interface{}{
		(*Network_NetworkEndpoints_FromCidr)(nil),
		(*Network_NetworkEndpoints_FromRegistry)(nil),
	}
}

// The gateway associated with this network. Traffic from remote networks
// will arrive at the specified gateway:port. All incoming traffic must
// use mTLS.
type Network_IstioNetworkGateway struct {
	// Types that are valid to be assigned to Gw:
	//	*Network_IstioNetworkGateway_RegistryServiceName
	//	*Network_IstioNetworkGateway_Address
	Gw isNetwork_IstioNetworkGateway_Gw `protobuf_oneof:"gw"`
	// The port associated with the gateway.
	Port uint32 `protobuf:"varint,3,opt,name=port,proto3" json:"port,omitempty"`
	// The locality associated with an explicitly specified gateway (i.e. ip)
	Locality             string   `protobuf:"bytes,4,opt,name=locality,proto3" json:"locality,omitempty"`
	XXX_NoUnkeyedLiteral struct{} `json:"-"`
	XXX_unrecognized     []byte   `json:"-"`
	XXX_sizecache        int32    `json:"-"`
}

func (m *Network_IstioNetworkGateway) Reset()         { *m = Network_IstioNetworkGateway{} }
func (m *Network_IstioNetworkGateway) String() string { return proto.CompactTextString(m) }
func (*Network_IstioNetworkGateway) ProtoMessage()    {}
func (*Network_IstioNetworkGateway) Descriptor() ([]byte, []int) {
	return fileDescriptor_a15df2a96e10cd86, []int{0, 1}
}
func (m *Network_IstioNetworkGateway) XXX_Unmarshal(b []byte) error {
	return m.Unmarshal(b)
}
func (m *Network_IstioNetworkGateway) XXX_Marshal(b []byte, deterministic bool) ([]byte, error) {
	if deterministic {
		return xxx_messageInfo_Network_IstioNetworkGateway.Marshal(b, m, deterministic)
	} else {
		b = b[:cap(b)]
		n, err := m.MarshalToSizedBuffer(b)
		if err != nil {
			return nil, err
		}
		return b[:n], nil
	}
}
func (m *Network_IstioNetworkGateway) XXX_Merge(src proto.Message) {
	xxx_messageInfo_Network_IstioNetworkGateway.Merge(m, src)
}
func (m *Network_IstioNetworkGateway) XXX_Size() int {
	return m.Size()
}
func (m *Network_IstioNetworkGateway) XXX_DiscardUnknown() {
	xxx_messageInfo_Network_IstioNetworkGateway.DiscardUnknown(m)
}

var xxx_messageInfo_Network_IstioNetworkGateway proto.InternalMessageInfo

type isNetwork_IstioNetworkGateway_Gw interface {
	isNetwork_IstioNetworkGateway_Gw()
	MarshalTo([]byte) (int, error)
	Size() int
}

type Network_IstioNetworkGateway_RegistryServiceName struct {
	RegistryServiceName string `protobuf:"bytes,1,opt,name=registry_service_name,json=registryServiceName,proto3,oneof"`
}
type Network_IstioNetworkGateway_Address struct {
	Address string `protobuf:"bytes,2,opt,name=address,proto3,oneof"`
}

func (*Network_IstioNetworkGateway_RegistryServiceName) isNetwork_IstioNetworkGateway_Gw() {}
func (*Network_IstioNetworkGateway_Address) isNetwork_IstioNetworkGateway_Gw()             {}

func (m *Network_IstioNetworkGateway) GetGw() isNetwork_IstioNetworkGateway_Gw {
	if m != nil {
		return m.Gw
	}
	return nil
}

func (m *Network_IstioNetworkGateway) GetRegistryServiceName() string {
	if x, ok := m.GetGw().(*Network_IstioNetworkGateway_RegistryServiceName); ok {
		return x.RegistryServiceName
	}
	return ""
}

func (m *Network_IstioNetworkGateway) GetAddress() string {
	if x, ok := m.GetGw().(*Network_IstioNetworkGateway_Address); ok {
		return x.Address
	}
	return ""
}

func (m *Network_IstioNetworkGateway) GetPort() uint32 {
	if m != nil {
		return m.Port
	}
	return 0
}

func (m *Network_IstioNetworkGateway) GetLocality() string {
	if m != nil {
		return m.Locality
	}
	return ""
}

// XXX_OneofWrappers is for the internal use of the proto package.
func (*Network_IstioNetworkGateway) XXX_OneofWrappers() []interface{} {
	return []interface{}{
		(*Network_IstioNetworkGateway_RegistryServiceName)(nil),
		(*Network_IstioNetworkGateway_Address)(nil),
	}
}

// MeshNetworks (config map) provides information about the set of networks
// inside a mesh and how to route to endpoints in each network. For example
//
// MeshNetworks(file/config map):
//
// ```yaml
// networks:
//   network1:
//   - endpoints:
//     - fromRegistry: registry1 #must match kubeconfig name in Kubernetes secret
//     - fromCidr: 192.168.100.0/22 #a VM network for example
//     gateways:
//     - registryServiceName: istio-ingressgateway.istio-system.svc.cluster.local
//       port: 15443
//       locality: us-east-1a
//     - address: 192.168.100.1
//       port: 15443
//       locality: us-east-1a
// ```
//
type MeshNetworks struct {
	// The set of networks inside this mesh. Each network should
	// have a unique name and information about how to infer the endpoints in
	// the network as well as the gateways associated with the network.
	Networks             map[string]*Network `protobuf:"bytes,1,rep,name=networks,proto3" json:"networks,omitempty" protobuf_key:"bytes,1,opt,name=key,proto3" protobuf_val:"bytes,2,opt,name=value,proto3"`
	XXX_NoUnkeyedLiteral struct{}            `json:"-"`
	XXX_unrecognized     []byte              `json:"-"`
	XXX_sizecache        int32               `json:"-"`
}

func (m *MeshNetworks) Reset()         { *m = MeshNetworks{} }
func (m *MeshNetworks) String() string { return proto.CompactTextString(m) }
func (*MeshNetworks) ProtoMessage()    {}
func (*MeshNetworks) Descriptor() ([]byte, []int) {
	return fileDescriptor_a15df2a96e10cd86, []int{1}
}
func (m *MeshNetworks) XXX_Unmarshal(b []byte) error {
	return m.Unmarshal(b)
}
func (m *MeshNetworks) XXX_Marshal(b []byte, deterministic bool) ([]byte, error) {
	if deterministic {
		return xxx_messageInfo_MeshNetworks.Marshal(b, m, deterministic)
	} else {
		b = b[:cap(b)]
		n, err := m.MarshalToSizedBuffer(b)
		if err != nil {
			return nil, err
		}
		return b[:n], nil
	}
}
func (m *MeshNetworks) XXX_Merge(src proto.Message) {
	xxx_messageInfo_MeshNetworks.Merge(m, src)
}
func (m *MeshNetworks) XXX_Size() int {
	return m.Size()
}
func (m *MeshNetworks) XXX_DiscardUnknown() {
	xxx_messageInfo_MeshNetworks.DiscardUnknown(m)
}

var xxx_messageInfo_MeshNetworks proto.InternalMessageInfo

func (m *MeshNetworks) GetNetworks() map[string]*Network {
	if m != nil {
		return m.Networks
	}
	return nil
}

func init() {
	proto.RegisterType((*Network)(nil), "istio.mesh.v1alpha1.Network")
	proto.RegisterType((*Network_NetworkEndpoints)(nil), "istio.mesh.v1alpha1.Network.NetworkEndpoints")
	proto.RegisterType((*Network_IstioNetworkGateway)(nil), "istio.mesh.v1alpha1.Network.IstioNetworkGateway")
	proto.RegisterType((*MeshNetworks)(nil), "istio.mesh.v1alpha1.MeshNetworks")
	proto.RegisterMapType((map[string]*Network)(nil), "istio.mesh.v1alpha1.MeshNetworks.NetworksEntry")
}

func init() { proto.RegisterFile("mesh/v1alpha1/network.proto", fileDescriptor_a15df2a96e10cd86) }

var fileDescriptor_a15df2a96e10cd86 = []byte{
	// 432 bytes of a gzipped FileDescriptorProto
	0x1f, 0x8b, 0x08, 0x00, 0x00, 0x00, 0x00, 0x00, 0x02, 0xff, 0x7c, 0x52, 0xcd, 0x6e, 0xd3, 0x40,
	0x10, 0x66, 0xed, 0x40, 0x93, 0x69, 0x23, 0x55, 0x1b, 0x21, 0x2c, 0x03, 0x21, 0xaa, 0x84, 0x94,
	0x0b, 0x36, 0x0d, 0x1c, 0x10, 0x37, 0x82, 0x2a, 0xe0, 0x40, 0x55, 0xcc, 0x09, 0x0e, 0x44, 0xdb,
	0x78, 0xea, 0xac, 0xea, 0x78, 0xad, 0xdd, 0xc5, 0x91, 0x5f, 0x87, 0x57, 0xe0, 0xc8, 0x0b, 0x70,
	0xe4, 0x11, 0xaa, 0x3c, 0x09, 0x5a, 0xaf, 0xd7, 0xd0, 0x2a, 0xea, 0xc9, 0x9e, 0xef, 0x9b, 0xef,
	0x9b, 0x9f, 0x1d, 0x78, 0xb8, 0x46, 0xb5, 0x8a, 0xab, 0x63, 0x96, 0x97, 0x2b, 0x76, 0x1c, 0x17,
	0xa8, 0x37, 0x42, 0x5e, 0x46, 0xa5, 0x14, 0x5a, 0xd0, 0x11, 0x57, 0x9a, 0x8b, 0xc8, 0xa4, 0x44,
	0x2e, 0x25, 0x7c, 0x92, 0x09, 0x91, 0xe5, 0x18, 0xb3, 0x92, 0xc7, 0x17, 0x1c, 0xf3, 0x74, 0x71,
	0x8e, 0x2b, 0x56, 0x71, 0x21, 0xad, 0xea, 0xe8, 0xa7, 0x0f, 0x7b, 0xa7, 0xd6, 0x87, 0x9e, 0xc1,
	0x00, 0x8b, 0xb4, 0x14, 0xbc, 0xd0, 0x2a, 0xf0, 0x26, 0xfe, 0x74, 0x7f, 0xf6, 0x2c, 0xda, 0xe1,
	0x1a, 0xb5, 0x02, 0xf7, 0x3d, 0x71, 0xa2, 0xb9, 0x7f, 0xf5, 0xc6, 0x4b, 0xfe, 0x99, 0xd0, 0x4f,
	0xd0, 0xcf, 0x98, 0xc6, 0x0d, 0xab, 0x55, 0xe0, 0x37, 0x86, 0xcf, 0x6f, 0x35, 0xfc, 0x60, 0xb8,
	0x36, 0x78, 0x67, 0x85, 0xd6, 0xb3, 0xb3, 0x09, 0xbf, 0xc1, 0xe1, 0xcd, 0xb2, 0xf4, 0x31, 0x0c,
	0x2e, 0xa4, 0x58, 0x2f, 0x96, 0x3c, 0x95, 0x01, 0x99, 0x90, 0xe9, 0xe0, 0xfd, 0x9d, 0xa4, 0x6f,
	0xa0, 0xb7, 0x3c, 0x95, 0xf4, 0x29, 0x0c, 0x1b, 0x5a, 0x62, 0xc6, 0x95, 0x96, 0x75, 0xe0, 0xb5,
	0x29, 0x07, 0x06, 0x4e, 0x5a, 0x74, 0xde, 0x03, 0xaf, 0xc0, 0xf0, 0x07, 0x81, 0xd1, 0x8e, 0x36,
	0xe8, 0x4b, 0xb8, 0xef, 0xf4, 0x0b, 0x85, 0xb2, 0xe2, 0x4b, 0x5c, 0x14, 0x6c, 0x8d, 0x5d, 0xbd,
	0x91, 0xa3, 0x3f, 0x5b, 0xf6, 0x94, 0xad, 0x91, 0x86, 0xb0, 0xc7, 0xd2, 0x54, 0xa2, 0x52, 0x5d,
	0x51, 0x07, 0xd0, 0x07, 0xd0, 0x2b, 0x85, 0xd4, 0x81, 0x3f, 0x21, 0xd3, 0xa1, 0x1d, 0xb3, 0x01,
	0x68, 0x08, 0xfd, 0x5c, 0x2c, 0x59, 0xce, 0x75, 0x1d, 0xf4, 0x8c, 0x2a, 0xe9, 0x62, 0xd3, 0x64,
	0xb6, 0x39, 0xfa, 0x45, 0xe0, 0xe0, 0x23, 0xaa, 0x55, 0xdb, 0xa3, 0xa2, 0x67, 0xd0, 0x6f, 0xaf,
	0x41, 0x05, 0xa4, 0x59, 0x74, 0xbc, 0x73, 0xd1, 0xff, 0x8b, 0xdc, 0xd6, 0xd5, 0x49, 0x61, 0xc6,
	0xb7, 0x7b, 0x76, 0x2e, 0xe1, 0x17, 0x18, 0x5e, 0xe3, 0xe9, 0x21, 0xf8, 0x97, 0x58, 0xdb, 0x71,
	0x13, 0xf3, 0x4b, 0x67, 0x70, 0xb7, 0x62, 0xf9, 0x77, 0x6c, 0x46, 0xdb, 0x9f, 0x3d, 0xba, 0xed,
	0x69, 0x13, 0x9b, 0xfa, 0xda, 0x7b, 0x45, 0xe6, 0xd3, 0xdf, 0xdb, 0x31, 0xf9, 0xb3, 0x1d, 0x93,
	0xab, 0xed, 0x98, 0x7c, 0x0d, 0xad, 0x8a, 0x8b, 0xe6, 0x48, 0xaf, 0x5d, 0xf8, 0xf9, 0xbd, 0xe6,
	0x48, 0x5f, 0xfc, 0x0d, 0x00, 0x00, 0xff, 0xff, 0x17, 0xcd, 0x07, 0x4e, 0xf9, 0x02, 0x00, 0x00,
}

func (m *Network) Marshal() (dAtA []byte, err error) {
	size := m.Size()
	dAtA = make([]byte, size)
	n, err := m.MarshalToSizedBuffer(dAtA[:size])
	if err != nil {
		return nil, err
	}
	return dAtA[:n], nil
}

func (m *Network) MarshalTo(dAtA []byte) (int, error) {
	size := m.Size()
	return m.MarshalToSizedBuffer(dAtA[:size])
}

func (m *Network) MarshalToSizedBuffer(dAtA []byte) (int, error) {
	i := len(dAtA)
	_ = i
	var l int
	_ = l
	if m.XXX_unrecognized != nil {
		i -= len(m.XXX_unrecognized)
		copy(dAtA[i:], m.XXX_unrecognized)
	}
	if len(m.Gateways) > 0 {
		for iNdEx := len(m.Gateways) - 1; iNdEx >= 0; iNdEx-- {
			{
				size, err := m.Gateways[iNdEx].MarshalToSizedBuffer(dAtA[:i])
				if err != nil {
					return 0, err
				}
				i -= size
				i = encodeVarintNetwork(dAtA, i, uint64(size))
			}
			i--
			dAtA[i] = 0x1a
		}
	}
	if len(m.Endpoints) > 0 {
		for iNdEx := len(m.Endpoints) - 1; iNdEx >= 0; iNdEx-- {
			{
				size, err := m.Endpoints[iNdEx].MarshalToSizedBuffer(dAtA[:i])
				if err != nil {
					return 0, err
				}
				i -= size
				i = encodeVarintNetwork(dAtA, i, uint64(size))
			}
			i--
			dAtA[i] = 0x12
		}
	}
	return len(dAtA) - i, nil
}

func (m *Network_NetworkEndpoints) Marshal() (dAtA []byte, err error) {
	size := m.Size()
	dAtA = make([]byte, size)
	n, err := m.MarshalToSizedBuffer(dAtA[:size])
	if err != nil {
		return nil, err
	}
	return dAtA[:n], nil
}

func (m *Network_NetworkEndpoints) MarshalTo(dAtA []byte) (int, error) {
	size := m.Size()
	return m.MarshalToSizedBuffer(dAtA[:size])
}

func (m *Network_NetworkEndpoints) MarshalToSizedBuffer(dAtA []byte) (int, error) {
	i := len(dAtA)
	_ = i
	var l int
	_ = l
	if m.XXX_unrecognized != nil {
		i -= len(m.XXX_unrecognized)
		copy(dAtA[i:], m.XXX_unrecognized)
	}
	if m.Ne != nil {
		{
			size := m.Ne.Size()
			i -= size
			if _, err := m.Ne.MarshalTo(dAtA[i:]); err != nil {
				return 0, err
			}
		}
	}
	return len(dAtA) - i, nil
}

func (m *Network_NetworkEndpoints_FromCidr) MarshalTo(dAtA []byte) (int, error) {
	return m.MarshalToSizedBuffer(dAtA[:m.Size()])
}

func (m *Network_NetworkEndpoints_FromCidr) MarshalToSizedBuffer(dAtA []byte) (int, error) {
	i := len(dAtA)
	i -= len(m.FromCidr)
	copy(dAtA[i:], m.FromCidr)
	i = encodeVarintNetwork(dAtA, i, uint64(len(m.FromCidr)))
	i--
	dAtA[i] = 0xa
	return len(dAtA) - i, nil
}
func (m *Network_NetworkEndpoints_FromRegistry) MarshalTo(dAtA []byte) (int, error) {
	return m.MarshalToSizedBuffer(dAtA[:m.Size()])
}

func (m *Network_NetworkEndpoints_FromRegistry) MarshalToSizedBuffer(dAtA []byte) (int, error) {
	i := len(dAtA)
	i -= len(m.FromRegistry)
	copy(dAtA[i:], m.FromRegistry)
	i = encodeVarintNetwork(dAtA, i, uint64(len(m.FromRegistry)))
	i--
	dAtA[i] = 0x12
	return len(dAtA) - i, nil
}
func (m *Network_IstioNetworkGateway) Marshal() (dAtA []byte, err error) {
	size := m.Size()
	dAtA = make([]byte, size)
	n, err := m.MarshalToSizedBuffer(dAtA[:size])
	if err != nil {
		return nil, err
	}
	return dAtA[:n], nil
}

func (m *Network_IstioNetworkGateway) MarshalTo(dAtA []byte) (int, error) {
	size := m.Size()
	return m.MarshalToSizedBuffer(dAtA[:size])
}

func (m *Network_IstioNetworkGateway) MarshalToSizedBuffer(dAtA []byte) (int, error) {
	i := len(dAtA)
	_ = i
	var l int
	_ = l
	if m.XXX_unrecognized != nil {
		i -= len(m.XXX_unrecognized)
		copy(dAtA[i:], m.XXX_unrecognized)
	}
	if len(m.Locality) > 0 {
		i -= len(m.Locality)
		copy(dAtA[i:], m.Locality)
		i = encodeVarintNetwork(dAtA, i, uint64(len(m.Locality)))
		i--
		dAtA[i] = 0x22
	}
	if m.Port != 0 {
		i = encodeVarintNetwork(dAtA, i, uint64(m.Port))
		i--
		dAtA[i] = 0x18
	}
	if m.Gw != nil {
		{
			size := m.Gw.Size()
			i -= size
			if _, err := m.Gw.MarshalTo(dAtA[i:]); err != nil {
				return 0, err
			}
		}
	}
	return len(dAtA) - i, nil
}

func (m *Network_IstioNetworkGateway_RegistryServiceName) MarshalTo(dAtA []byte) (int, error) {
	return m.MarshalToSizedBuffer(dAtA[:m.Size()])
}

func (m *Network_IstioNetworkGateway_RegistryServiceName) MarshalToSizedBuffer(dAtA []byte) (int, error) {
	i := len(dAtA)
	i -= len(m.RegistryServiceName)
	copy(dAtA[i:], m.RegistryServiceName)
	i = encodeVarintNetwork(dAtA, i, uint64(len(m.RegistryServiceName)))
	i--
	dAtA[i] = 0xa
	return len(dAtA) - i, nil
}
func (m *Network_IstioNetworkGateway_Address) MarshalTo(dAtA []byte) (int, error) {
	return m.MarshalToSizedBuffer(dAtA[:m.Size()])
}

func (m *Network_IstioNetworkGateway_Address) MarshalToSizedBuffer(dAtA []byte) (int, error) {
	i := len(dAtA)
	i -= len(m.Address)
	copy(dAtA[i:], m.Address)
	i = encodeVarintNetwork(dAtA, i, uint64(len(m.Address)))
	i--
	dAtA[i] = 0x12
	return len(dAtA) - i, nil
}
func (m *MeshNetworks) Marshal() (dAtA []byte, err error) {
	size := m.Size()
	dAtA = make([]byte, size)
	n, err := m.MarshalToSizedBuffer(dAtA[:size])
	if err != nil {
		return nil, err
	}
	return dAtA[:n], nil
}

func (m *MeshNetworks) MarshalTo(dAtA []byte) (int, error) {
	size := m.Size()
	return m.MarshalToSizedBuffer(dAtA[:size])
}

func (m *MeshNetworks) MarshalToSizedBuffer(dAtA []byte) (int, error) {
	i := len(dAtA)
	_ = i
	var l int
	_ = l
	if m.XXX_unrecognized != nil {
		i -= len(m.XXX_unrecognized)
		copy(dAtA[i:], m.XXX_unrecognized)
	}
	if len(m.Networks) > 0 {
		for k := range m.Networks {
			v := m.Networks[k]
			baseI := i
			if v != nil {
				{
					size, err := v.MarshalToSizedBuffer(dAtA[:i])
					if err != nil {
						return 0, err
					}
					i -= size
					i = encodeVarintNetwork(dAtA, i, uint64(size))
				}
				i--
				dAtA[i] = 0x12
			}
			i -= len(k)
			copy(dAtA[i:], k)
			i = encodeVarintNetwork(dAtA, i, uint64(len(k)))
			i--
			dAtA[i] = 0xa
			i = encodeVarintNetwork(dAtA, i, uint64(baseI-i))
			i--
			dAtA[i] = 0xa
		}
	}
	return len(dAtA) - i, nil
}

func encodeVarintNetwork(dAtA []byte, offset int, v uint64) int {
	offset -= sovNetwork(v)
	base := offset
	for v >= 1<<7 {
		dAtA[offset] = uint8(v&0x7f | 0x80)
		v >>= 7
		offset++
	}
	dAtA[offset] = uint8(v)
	return base
}
func (m *Network) Size() (n int) {
	if m == nil {
		return 0
	}
	var l int
	_ = l
	if len(m.Endpoints) > 0 {
		for _, e := range m.Endpoints {
			l = e.Size()
			n += 1 + l + sovNetwork(uint64(l))
		}
	}
	if len(m.Gateways) > 0 {
		for _, e := range m.Gateways {
			l = e.Size()
			n += 1 + l + sovNetwork(uint64(l))
		}
	}
	if m.XXX_unrecognized != nil {
		n += len(m.XXX_unrecognized)
	}
	return n
}

func (m *Network_NetworkEndpoints) Size() (n int) {
	if m == nil {
		return 0
	}
	var l int
	_ = l
	if m.Ne != nil {
		n += m.Ne.Size()
	}
	if m.XXX_unrecognized != nil {
		n += len(m.XXX_unrecognized)
	}
	return n
}

func (m *Network_NetworkEndpoints_FromCidr) Size() (n int) {
	if m == nil {
		return 0
	}
	var l int
	_ = l
	l = len(m.FromCidr)
	n += 1 + l + sovNetwork(uint64(l))
	return n
}
func (m *Network_NetworkEndpoints_FromRegistry) Size() (n int) {
	if m == nil {
		return 0
	}
	var l int
	_ = l
	l = len(m.FromRegistry)
	n += 1 + l + sovNetwork(uint64(l))
	return n
}
func (m *Network_IstioNetworkGateway) Size() (n int) {
	if m == nil {
		return 0
	}
	var l int
	_ = l
	if m.Gw != nil {
		n += m.Gw.Size()
	}
	if m.Port != 0 {
		n += 1 + sovNetwork(uint64(m.Port))
	}
	l = len(m.Locality)
	if l > 0 {
		n += 1 + l + sovNetwork(uint64(l))
	}
	if m.XXX_unrecognized != nil {
		n += len(m.XXX_unrecognized)
	}
	return n
}

func (m *Network_IstioNetworkGateway_RegistryServiceName) Size() (n int) {
	if m == nil {
		return 0
	}
	var l int
	_ = l
	l = len(m.RegistryServiceName)
	n += 1 + l + sovNetwork(uint64(l))
	return n
}
func (m *Network_IstioNetworkGateway_Address) Size() (n int) {
	if m == nil {
		return 0
	}
	var l int
	_ = l
	l = len(m.Address)
	n += 1 + l + sovNetwork(uint64(l))
	return n
}
func (m *MeshNetworks) Size() (n int) {
	if m == nil {
		return 0
	}
	var l int
	_ = l
	if len(m.Networks) > 0 {
		for k, v := range m.Networks {
			_ = k
			_ = v
			l = 0
			if v != nil {
				l = v.Size()
				l += 1 + sovNetwork(uint64(l))
			}
			mapEntrySize := 1 + len(k) + sovNetwork(uint64(len(k))) + l
			n += mapEntrySize + 1 + sovNetwork(uint64(mapEntrySize))
		}
	}
	if m.XXX_unrecognized != nil {
		n += len(m.XXX_unrecognized)
	}
	return n
}

func sovNetwork(x uint64) (n int) {
	return (math_bits.Len64(x|1) + 6) / 7
}
func sozNetwork(x uint64) (n int) {
	return sovNetwork(uint64((x << 1) ^ uint64((int64(x) >> 63))))
}
func (m *Network) Unmarshal(dAtA []byte) error {
	l := len(dAtA)
	iNdEx := 0
	for iNdEx < l {
		preIndex := iNdEx
		var wire uint64
		for shift := uint(0); ; shift += 7 {
			if shift >= 64 {
				return ErrIntOverflowNetwork
			}
			if iNdEx >= l {
				return io.ErrUnexpectedEOF
			}
			b := dAtA[iNdEx]
			iNdEx++
			wire |= uint64(b&0x7F) << shift
			if b < 0x80 {
				break
			}
		}
		fieldNum := int32(wire >> 3)
		wireType := int(wire & 0x7)
		if wireType == 4 {
			return fmt.Errorf("proto: Network: wiretype end group for non-group")
		}
		if fieldNum <= 0 {
			return fmt.Errorf("proto: Network: illegal tag %d (wire type %d)", fieldNum, wire)
		}
		switch fieldNum {
		case 2:
			if wireType != 2 {
				return fmt.Errorf("proto: wrong wireType = %d for field Endpoints", wireType)
			}
			var msglen int
			for shift := uint(0); ; shift += 7 {
				if shift >= 64 {
					return ErrIntOverflowNetwork
				}
				if iNdEx >= l {
					return io.ErrUnexpectedEOF
				}
				b := dAtA[iNdEx]
				iNdEx++
				msglen |= int(b&0x7F) << shift
				if b < 0x80 {
					break
				}
			}
			if msglen < 0 {
				return ErrInvalidLengthNetwork
			}
			postIndex := iNdEx + msglen
			if postIndex < 0 {
				return ErrInvalidLengthNetwork
			}
			if postIndex > l {
				return io.ErrUnexpectedEOF
			}
			m.Endpoints = append(m.Endpoints, &Network_NetworkEndpoints{})
			if err := m.Endpoints[len(m.Endpoints)-1].Unmarshal(dAtA[iNdEx:postIndex]); err != nil {
				return err
			}
			iNdEx = postIndex
		case 3:
			if wireType != 2 {
				return fmt.Errorf("proto: wrong wireType = %d for field Gateways", wireType)
			}
			var msglen int
			for shift := uint(0); ; shift += 7 {
				if shift >= 64 {
					return ErrIntOverflowNetwork
				}
				if iNdEx >= l {
					return io.ErrUnexpectedEOF
				}
				b := dAtA[iNdEx]
				iNdEx++
				msglen |= int(b&0x7F) << shift
				if b < 0x80 {
					break
				}
			}
			if msglen < 0 {
				return ErrInvalidLengthNetwork
			}
			postIndex := iNdEx + msglen
			if postIndex < 0 {
				return ErrInvalidLengthNetwork
			}
			if postIndex > l {
				return io.ErrUnexpectedEOF
			}
			m.Gateways = append(m.Gateways, &Network_IstioNetworkGateway{})
			if err := m.Gateways[len(m.Gateways)-1].Unmarshal(dAtA[iNdEx:postIndex]); err != nil {
				return err
			}
			iNdEx = postIndex
		default:
			iNdEx = preIndex
			skippy, err := skipNetwork(dAtA[iNdEx:])
			if err != nil {
				return err
			}
			if skippy < 0 {
				return ErrInvalidLengthNetwork
			}
			if (iNdEx + skippy) < 0 {
				return ErrInvalidLengthNetwork
			}
			if (iNdEx + skippy) > l {
				return io.ErrUnexpectedEOF
			}
			m.XXX_unrecognized = append(m.XXX_unrecognized, dAtA[iNdEx:iNdEx+skippy]...)
			iNdEx += skippy
		}
	}

	if iNdEx > l {
		return io.ErrUnexpectedEOF
	}
	return nil
}
func (m *Network_NetworkEndpoints) Unmarshal(dAtA []byte) error {
	l := len(dAtA)
	iNdEx := 0
	for iNdEx < l {
		preIndex := iNdEx
		var wire uint64
		for shift := uint(0); ; shift += 7 {
			if shift >= 64 {
				return ErrIntOverflowNetwork
			}
			if iNdEx >= l {
				return io.ErrUnexpectedEOF
			}
			b := dAtA[iNdEx]
			iNdEx++
			wire |= uint64(b&0x7F) << shift
			if b < 0x80 {
				break
			}
		}
		fieldNum := int32(wire >> 3)
		wireType := int(wire & 0x7)
		if wireType == 4 {
			return fmt.Errorf("proto: NetworkEndpoints: wiretype end group for non-group")
		}
		if fieldNum <= 0 {
			return fmt.Errorf("proto: NetworkEndpoints: illegal tag %d (wire type %d)", fieldNum, wire)
		}
		switch fieldNum {
		case 1:
			if wireType != 2 {
				return fmt.Errorf("proto: wrong wireType = %d for field FromCidr", wireType)
			}
			var stringLen uint64
			for shift := uint(0); ; shift += 7 {
				if shift >= 64 {
					return ErrIntOverflowNetwork
				}
				if iNdEx >= l {
					return io.ErrUnexpectedEOF
				}
				b := dAtA[iNdEx]
				iNdEx++
				stringLen |= uint64(b&0x7F) << shift
				if b < 0x80 {
					break
				}
			}
			intStringLen := int(stringLen)
			if intStringLen < 0 {
				return ErrInvalidLengthNetwork
			}
			postIndex := iNdEx + intStringLen
			if postIndex < 0 {
				return ErrInvalidLengthNetwork
			}
			if postIndex > l {
				return io.ErrUnexpectedEOF
			}
			m.Ne = &Network_NetworkEndpoints_FromCidr{string(dAtA[iNdEx:postIndex])}
			iNdEx = postIndex
		case 2:
			if wireType != 2 {
				return fmt.Errorf("proto: wrong wireType = %d for field FromRegistry", wireType)
			}
			var stringLen uint64
			for shift := uint(0); ; shift += 7 {
				if shift >= 64 {
					return ErrIntOverflowNetwork
				}
				if iNdEx >= l {
					return io.ErrUnexpectedEOF
				}
				b := dAtA[iNdEx]
				iNdEx++
				stringLen |= uint64(b&0x7F) << shift
				if b < 0x80 {
					break
				}
			}
			intStringLen := int(stringLen)
			if intStringLen < 0 {
				return ErrInvalidLengthNetwork
			}
			postIndex := iNdEx + intStringLen
			if postIndex < 0 {
				return ErrInvalidLengthNetwork
			}
			if postIndex > l {
				return io.ErrUnexpectedEOF
			}
			m.Ne = &Network_NetworkEndpoints_FromRegistry{string(dAtA[iNdEx:postIndex])}
			iNdEx = postIndex
		default:
			iNdEx = preIndex
			skippy, err := skipNetwork(dAtA[iNdEx:])
			if err != nil {
				return err
			}
			if skippy < 0 {
				return ErrInvalidLengthNetwork
			}
			if (iNdEx + skippy) < 0 {
				return ErrInvalidLengthNetwork
			}
			if (iNdEx + skippy) > l {
				return io.ErrUnexpectedEOF
			}
			m.XXX_unrecognized = append(m.XXX_unrecognized, dAtA[iNdEx:iNdEx+skippy]...)
			iNdEx += skippy
		}
	}

	if iNdEx > l {
		return io.ErrUnexpectedEOF
	}
	return nil
}
func (m *Network_IstioNetworkGateway) Unmarshal(dAtA []byte) error {
	l := len(dAtA)
	iNdEx := 0
	for iNdEx < l {
		preIndex := iNdEx
		var wire uint64
		for shift := uint(0); ; shift += 7 {
			if shift >= 64 {
				return ErrIntOverflowNetwork
			}
			if iNdEx >= l {
				return io.ErrUnexpectedEOF
			}
			b := dAtA[iNdEx]
			iNdEx++
			wire |= uint64(b&0x7F) << shift
			if b < 0x80 {
				break
			}
		}
		fieldNum := int32(wire >> 3)
		wireType := int(wire & 0x7)
		if wireType == 4 {
			return fmt.Errorf("proto: IstioNetworkGateway: wiretype end group for non-group")
		}
		if fieldNum <= 0 {
			return fmt.Errorf("proto: IstioNetworkGateway: illegal tag %d (wire type %d)", fieldNum, wire)
		}
		switch fieldNum {
		case 1:
			if wireType != 2 {
				return fmt.Errorf("proto: wrong wireType = %d for field RegistryServiceName", wireType)
			}
			var stringLen uint64
			for shift := uint(0); ; shift += 7 {
				if shift >= 64 {
					return ErrIntOverflowNetwork
				}
				if iNdEx >= l {
					return io.ErrUnexpectedEOF
				}
				b := dAtA[iNdEx]
				iNdEx++
				stringLen |= uint64(b&0x7F) << shift
				if b < 0x80 {
					break
				}
			}
			intStringLen := int(stringLen)
			if intStringLen < 0 {
				return ErrInvalidLengthNetwork
			}
			postIndex := iNdEx + intStringLen
			if postIndex < 0 {
				return ErrInvalidLengthNetwork
			}
			if postIndex > l {
				return io.ErrUnexpectedEOF
			}
			m.Gw = &Network_IstioNetworkGateway_RegistryServiceName{string(dAtA[iNdEx:postIndex])}
			iNdEx = postIndex
		case 2:
			if wireType != 2 {
				return fmt.Errorf("proto: wrong wireType = %d for field Address", wireType)
			}
			var stringLen uint64
			for shift := uint(0); ; shift += 7 {
				if shift >= 64 {
					return ErrIntOverflowNetwork
				}
				if iNdEx >= l {
					return io.ErrUnexpectedEOF
				}
				b := dAtA[iNdEx]
				iNdEx++
				stringLen |= uint64(b&0x7F) << shift
				if b < 0x80 {
					break
				}
			}
			intStringLen := int(stringLen)
			if intStringLen < 0 {
				return ErrInvalidLengthNetwork
			}
			postIndex := iNdEx + intStringLen
			if postIndex < 0 {
				return ErrInvalidLengthNetwork
			}
			if postIndex > l {
				return io.ErrUnexpectedEOF
			}
			m.Gw = &Network_IstioNetworkGateway_Address{string(dAtA[iNdEx:postIndex])}
			iNdEx = postIndex
		case 3:
			if wireType != 0 {
				return fmt.Errorf("proto: wrong wireType = %d for field Port", wireType)
			}
			m.Port = 0
			for shift := uint(0); ; shift += 7 {
				if shift >= 64 {
					return ErrIntOverflowNetwork
				}
				if iNdEx >= l {
					return io.ErrUnexpectedEOF
				}
				b := dAtA[iNdEx]
				iNdEx++
				m.Port |= uint32(b&0x7F) << shift
				if b < 0x80 {
					break
				}
			}
		case 4:
			if wireType != 2 {
				return fmt.Errorf("proto: wrong wireType = %d for field Locality", wireType)
			}
			var stringLen uint64
			for shift := uint(0); ; shift += 7 {
				if shift >= 64 {
					return ErrIntOverflowNetwork
				}
				if iNdEx >= l {
					return io.ErrUnexpectedEOF
				}
				b := dAtA[iNdEx]
				iNdEx++
				stringLen |= uint64(b&0x7F) << shift
				if b < 0x80 {
					break
				}
			}
			intStringLen := int(stringLen)
			if intStringLen < 0 {
				return ErrInvalidLengthNetwork
			}
			postIndex := iNdEx + intStringLen
			if postIndex < 0 {
				return ErrInvalidLengthNetwork
			}
			if postIndex > l {
				return io.ErrUnexpectedEOF
			}
			m.Locality = string(dAtA[iNdEx:postIndex])
			iNdEx = postIndex
		default:
			iNdEx = preIndex
			skippy, err := skipNetwork(dAtA[iNdEx:])
			if err != nil {
				return err
			}
			if skippy < 0 {
				return ErrInvalidLengthNetwork
			}
			if (iNdEx + skippy) < 0 {
				return ErrInvalidLengthNetwork
			}
			if (iNdEx + skippy) > l {
				return io.ErrUnexpectedEOF
			}
			m.XXX_unrecognized = append(m.XXX_unrecognized, dAtA[iNdEx:iNdEx+skippy]...)
			iNdEx += skippy
		}
	}

	if iNdEx > l {
		return io.ErrUnexpectedEOF
	}
	return nil
}
func (m *MeshNetworks) Unmarshal(dAtA []byte) error {
	l := len(dAtA)
	iNdEx := 0
	for iNdEx < l {
		preIndex := iNdEx
		var wire uint64
		for shift := uint(0); ; shift += 7 {
			if shift >= 64 {
				return ErrIntOverflowNetwork
			}
			if iNdEx >= l {
				return io.ErrUnexpectedEOF
			}
			b := dAtA[iNdEx]
			iNdEx++
			wire |= uint64(b&0x7F) << shift
			if b < 0x80 {
				break
			}
		}
		fieldNum := int32(wire >> 3)
		wireType := int(wire & 0x7)
		if wireType == 4 {
			return fmt.Errorf("proto: MeshNetworks: wiretype end group for non-group")
		}
		if fieldNum <= 0 {
			return fmt.Errorf("proto: MeshNetworks: illegal tag %d (wire type %d)", fieldNum, wire)
		}
		switch fieldNum {
		case 1:
			if wireType != 2 {
				return fmt.Errorf("proto: wrong wireType = %d for field Networks", wireType)
			}
			var msglen int
			for shift := uint(0); ; shift += 7 {
				if shift >= 64 {
					return ErrIntOverflowNetwork
				}
				if iNdEx >= l {
					return io.ErrUnexpectedEOF
				}
				b := dAtA[iNdEx]
				iNdEx++
				msglen |= int(b&0x7F) << shift
				if b < 0x80 {
					break
				}
			}
			if msglen < 0 {
				return ErrInvalidLengthNetwork
			}
			postIndex := iNdEx + msglen
			if postIndex < 0 {
				return ErrInvalidLengthNetwork
			}
			if postIndex > l {
				return io.ErrUnexpectedEOF
			}
			if m.Networks == nil {
				m.Networks = make(map[string]*Network)
			}
			var mapkey string
			var mapvalue *Network
			for iNdEx < postIndex {
				entryPreIndex := iNdEx
				var wire uint64
				for shift := uint(0); ; shift += 7 {
					if shift >= 64 {
						return ErrIntOverflowNetwork
					}
					if iNdEx >= l {
						return io.ErrUnexpectedEOF
					}
					b := dAtA[iNdEx]
					iNdEx++
					wire |= uint64(b&0x7F) << shift
					if b < 0x80 {
						break
					}
				}
				fieldNum := int32(wire >> 3)
				if fieldNum == 1 {
					var stringLenmapkey uint64
					for shift := uint(0); ; shift += 7 {
						if shift >= 64 {
							return ErrIntOverflowNetwork
						}
						if iNdEx >= l {
							return io.ErrUnexpectedEOF
						}
						b := dAtA[iNdEx]
						iNdEx++
						stringLenmapkey |= uint64(b&0x7F) << shift
						if b < 0x80 {
							break
						}
					}
					intStringLenmapkey := int(stringLenmapkey)
					if intStringLenmapkey < 0 {
						return ErrInvalidLengthNetwork
					}
					postStringIndexmapkey := iNdEx + intStringLenmapkey
					if postStringIndexmapkey < 0 {
						return ErrInvalidLengthNetwork
					}
					if postStringIndexmapkey > l {
						return io.ErrUnexpectedEOF
					}
					mapkey = string(dAtA[iNdEx:postStringIndexmapkey])
					iNdEx = postStringIndexmapkey
				} else if fieldNum == 2 {
					var mapmsglen int
					for shift := uint(0); ; shift += 7 {
						if shift >= 64 {
							return ErrIntOverflowNetwork
						}
						if iNdEx >= l {
							return io.ErrUnexpectedEOF
						}
						b := dAtA[iNdEx]
						iNdEx++
						mapmsglen |= int(b&0x7F) << shift
						if b < 0x80 {
							break
						}
					}
					if mapmsglen < 0 {
						return ErrInvalidLengthNetwork
					}
					postmsgIndex := iNdEx + mapmsglen
					if postmsgIndex < 0 {
						return ErrInvalidLengthNetwork
					}
					if postmsgIndex > l {
						return io.ErrUnexpectedEOF
					}
					mapvalue = &Network{}
					if err := mapvalue.Unmarshal(dAtA[iNdEx:postmsgIndex]); err != nil {
						return err
					}
					iNdEx = postmsgIndex
				} else {
					iNdEx = entryPreIndex
					skippy, err := skipNetwork(dAtA[iNdEx:])
					if err != nil {
						return err
					}
					if skippy < 0 {
						return ErrInvalidLengthNetwork
					}
					if (iNdEx + skippy) > postIndex {
						return io.ErrUnexpectedEOF
					}
					iNdEx += skippy
				}
			}
			m.Networks[mapkey] = mapvalue
			iNdEx = postIndex
		default:
			iNdEx = preIndex
			skippy, err := skipNetwork(dAtA[iNdEx:])
			if err != nil {
				return err
			}
			if skippy < 0 {
				return ErrInvalidLengthNetwork
			}
			if (iNdEx + skippy) < 0 {
				return ErrInvalidLengthNetwork
			}
			if (iNdEx + skippy) > l {
				return io.ErrUnexpectedEOF
			}
			m.XXX_unrecognized = append(m.XXX_unrecognized, dAtA[iNdEx:iNdEx+skippy]...)
			iNdEx += skippy
		}
	}

	if iNdEx > l {
		return io.ErrUnexpectedEOF
	}
	return nil
}
func skipNetwork(dAtA []byte) (n int, err error) {
	l := len(dAtA)
	iNdEx := 0
	for iNdEx < l {
		var wire uint64
		for shift := uint(0); ; shift += 7 {
			if shift >= 64 {
				return 0, ErrIntOverflowNetwork
			}
			if iNdEx >= l {
				return 0, io.ErrUnexpectedEOF
			}
			b := dAtA[iNdEx]
			iNdEx++
			wire |= (uint64(b) & 0x7F) << shift
			if b < 0x80 {
				break
			}
		}
		wireType := int(wire & 0x7)
		switch wireType {
		case 0:
			for shift := uint(0); ; shift += 7 {
				if shift >= 64 {
					return 0, ErrIntOverflowNetwork
				}
				if iNdEx >= l {
					return 0, io.ErrUnexpectedEOF
				}
				iNdEx++
				if dAtA[iNdEx-1] < 0x80 {
					break
				}
			}
			return iNdEx, nil
		case 1:
			iNdEx += 8
			return iNdEx, nil
		case 2:
			var length int
			for shift := uint(0); ; shift += 7 {
				if shift >= 64 {
					return 0, ErrIntOverflowNetwork
				}
				if iNdEx >= l {
					return 0, io.ErrUnexpectedEOF
				}
				b := dAtA[iNdEx]
				iNdEx++
				length |= (int(b) & 0x7F) << shift
				if b < 0x80 {
					break
				}
			}
			if length < 0 {
				return 0, ErrInvalidLengthNetwork
			}
			iNdEx += length
			if iNdEx < 0 {
				return 0, ErrInvalidLengthNetwork
			}
			return iNdEx, nil
		case 3:
			for {
				var innerWire uint64
				var start int = iNdEx
				for shift := uint(0); ; shift += 7 {
					if shift >= 64 {
						return 0, ErrIntOverflowNetwork
					}
					if iNdEx >= l {
						return 0, io.ErrUnexpectedEOF
					}
					b := dAtA[iNdEx]
					iNdEx++
					innerWire |= (uint64(b) & 0x7F) << shift
					if b < 0x80 {
						break
					}
				}
				innerWireType := int(innerWire & 0x7)
				if innerWireType == 4 {
					break
				}
				next, err := skipNetwork(dAtA[start:])
				if err != nil {
					return 0, err
				}
				iNdEx = start + next
				if iNdEx < 0 {
					return 0, ErrInvalidLengthNetwork
				}
			}
			return iNdEx, nil
		case 4:
			return iNdEx, nil
		case 5:
			iNdEx += 4
			return iNdEx, nil
		default:
			return 0, fmt.Errorf("proto: illegal wireType %d", wireType)
		}
	}
	panic("unreachable")
}

var (
	ErrInvalidLengthNetwork = fmt.Errorf("proto: negative length found during unmarshaling")
	ErrIntOverflowNetwork   = fmt.Errorf("proto: integer overflow")
)
