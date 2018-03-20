// Code generated by protoc-gen-go. DO NOT EDIT.
// source: provider_register.proto

/*
Package provider_client_pb is a generated protocol buffer package.

It is generated from these files:
	provider_register.proto

It has these top-level messages:
	Empty
	PublicKeyResp
	RegisterReq
	RegisterResp
*/
package provider_client_pb

import proto "github.com/golang/protobuf/proto"
import fmt "fmt"
import math "math"

import (
	context "golang.org/x/net/context"
	grpc "google.golang.org/grpc"
)

// Reference imports to suppress errors if they are not otherwise used.
var _ = proto.Marshal
var _ = fmt.Errorf
var _ = math.Inf

// This is a compile-time assertion to ensure that this generated file
// is compatible with the proto package it is being compiled against.
// A compilation error at this line likely means your copy of the
// proto package needs to be updated.
const _ = proto.ProtoPackageIsVersion2 // please upgrade the proto package

type CurrencyType int32

const (
	CurrencyType_SPO CurrencyType = 0
	CurrencyType_SKY CurrencyType = 1
)

var CurrencyType_name = map[int32]string{
	0: "SPO",
	1: "SKY",
}
var CurrencyType_value = map[string]int32{
	"SPO": 0,
	"SKY": 1,
}

func (x CurrencyType) String() string {
	return proto.EnumName(CurrencyType_name, int32(x))
}
func (CurrencyType) EnumDescriptor() ([]byte, []int) { return fileDescriptor0, []int{0} }

type Empty struct {
}

func (m *Empty) Reset()                    { *m = Empty{} }
func (m *Empty) String() string            { return proto.CompactTextString(m) }
func (*Empty) ProtoMessage()               {}
func (*Empty) Descriptor() ([]byte, []int) { return fileDescriptor0, []int{0} }

type PublicKeyResp struct {
	PublicKey []byte `protobuf:"bytes,1,opt,name=publicKey,proto3" json:"publicKey,omitempty"`
}

func (m *PublicKeyResp) Reset()                    { *m = PublicKeyResp{} }
func (m *PublicKeyResp) String() string            { return proto.CompactTextString(m) }
func (*PublicKeyResp) ProtoMessage()               {}
func (*PublicKeyResp) Descriptor() ([]byte, []int) { return fileDescriptor0, []int{1} }

func (m *PublicKeyResp) GetPublicKey() []byte {
	if m != nil {
		return m.PublicKey
	}
	return nil
}

type RegisterReq struct {
	NodeId            string       `protobuf:"bytes,1,opt,name=nodeId" json:"nodeId,omitempty"`
	PublicKey         []byte       `protobuf:"bytes,2,opt,name=publicKey,proto3" json:"publicKey,omitempty"`
	CurrencyType      CurrencyType `protobuf:"varint,3,opt,name=currencyType,enum=provider.client.pb.CurrencyType" json:"currencyType,omitempty"`
	WalletAddress     string       `protobuf:"bytes,4,opt,name=walletAddress" json:"walletAddress,omitempty"`
	BillEmail         string       `protobuf:"bytes,5,opt,name=billEmail" json:"billEmail,omitempty"`
	StorageVolume     uint64       `protobuf:"varint,6,opt,name=storageVolume" json:"storageVolume,omitempty"`
	UpBandwidth       uint64       `protobuf:"varint,7,opt,name=upBandwidth" json:"upBandwidth,omitempty"`
	DownBandwidth     uint64       `protobuf:"varint,8,opt,name=downBandwidth" json:"downBandwidth,omitempty"`
	TestUpBandwidth   uint64       `protobuf:"varint,9,opt,name=testUpBandwidth" json:"testUpBandwidth,omitempty"`
	TestDownBandwidth uint64       `protobuf:"varint,10,opt,name=testDownBandwidth" json:"testDownBandwidth,omitempty"`
	Availability      float32      `protobuf:"fixed32,11,opt,name=availability" json:"availability,omitempty"`
}

func (m *RegisterReq) Reset()                    { *m = RegisterReq{} }
func (m *RegisterReq) String() string            { return proto.CompactTextString(m) }
func (*RegisterReq) ProtoMessage()               {}
func (*RegisterReq) Descriptor() ([]byte, []int) { return fileDescriptor0, []int{2} }

func (m *RegisterReq) GetNodeId() string {
	if m != nil {
		return m.NodeId
	}
	return ""
}

func (m *RegisterReq) GetPublicKey() []byte {
	if m != nil {
		return m.PublicKey
	}
	return nil
}

func (m *RegisterReq) GetCurrencyType() CurrencyType {
	if m != nil {
		return m.CurrencyType
	}
	return CurrencyType_SPO
}

func (m *RegisterReq) GetWalletAddress() string {
	if m != nil {
		return m.WalletAddress
	}
	return ""
}

func (m *RegisterReq) GetBillEmail() string {
	if m != nil {
		return m.BillEmail
	}
	return ""
}

func (m *RegisterReq) GetStorageVolume() uint64 {
	if m != nil {
		return m.StorageVolume
	}
	return 0
}

func (m *RegisterReq) GetUpBandwidth() uint64 {
	if m != nil {
		return m.UpBandwidth
	}
	return 0
}

func (m *RegisterReq) GetDownBandwidth() uint64 {
	if m != nil {
		return m.DownBandwidth
	}
	return 0
}

func (m *RegisterReq) GetTestUpBandwidth() uint64 {
	if m != nil {
		return m.TestUpBandwidth
	}
	return 0
}

func (m *RegisterReq) GetTestDownBandwidth() uint64 {
	if m != nil {
		return m.TestDownBandwidth
	}
	return 0
}

func (m *RegisterReq) GetAvailability() float32 {
	if m != nil {
		return m.Availability
	}
	return 0
}

type RegisterResp struct {
	Success bool `protobuf:"varint,1,opt,name=success" json:"success,omitempty"`
}

func (m *RegisterResp) Reset()                    { *m = RegisterResp{} }
func (m *RegisterResp) String() string            { return proto.CompactTextString(m) }
func (*RegisterResp) ProtoMessage()               {}
func (*RegisterResp) Descriptor() ([]byte, []int) { return fileDescriptor0, []int{3} }

func (m *RegisterResp) GetSuccess() bool {
	if m != nil {
		return m.Success
	}
	return false
}

func init() {
	proto.RegisterType((*Empty)(nil), "provider.client.pb.Empty")
	proto.RegisterType((*PublicKeyResp)(nil), "provider.client.pb.PublicKeyResp")
	proto.RegisterType((*RegisterReq)(nil), "provider.client.pb.RegisterReq")
	proto.RegisterType((*RegisterResp)(nil), "provider.client.pb.RegisterResp")
	proto.RegisterEnum("provider.client.pb.CurrencyType", CurrencyType_name, CurrencyType_value)
}

// Reference imports to suppress errors if they are not otherwise used.
var _ context.Context
var _ grpc.ClientConn

// This is a compile-time assertion to ensure that this generated file
// is compatible with the grpc package it is being compiled against.
const _ = grpc.SupportPackageIsVersion4

// Client API for ProviderRegisterService service

type ProviderRegisterServiceClient interface {
	GetPublicKey(ctx context.Context, in *Empty, opts ...grpc.CallOption) (*PublicKeyResp, error)
	Register(ctx context.Context, in *RegisterReq, opts ...grpc.CallOption) (*RegisterResp, error)
}

type providerRegisterServiceClient struct {
	cc *grpc.ClientConn
}

func NewProviderRegisterServiceClient(cc *grpc.ClientConn) ProviderRegisterServiceClient {
	return &providerRegisterServiceClient{cc}
}

func (c *providerRegisterServiceClient) GetPublicKey(ctx context.Context, in *Empty, opts ...grpc.CallOption) (*PublicKeyResp, error) {
	out := new(PublicKeyResp)
	err := grpc.Invoke(ctx, "/provider.client.pb.ProviderRegisterService/GetPublicKey", in, out, c.cc, opts...)
	if err != nil {
		return nil, err
	}
	return out, nil
}

func (c *providerRegisterServiceClient) Register(ctx context.Context, in *RegisterReq, opts ...grpc.CallOption) (*RegisterResp, error) {
	out := new(RegisterResp)
	err := grpc.Invoke(ctx, "/provider.client.pb.ProviderRegisterService/Register", in, out, c.cc, opts...)
	if err != nil {
		return nil, err
	}
	return out, nil
}

// Server API for ProviderRegisterService service

type ProviderRegisterServiceServer interface {
	GetPublicKey(context.Context, *Empty) (*PublicKeyResp, error)
	Register(context.Context, *RegisterReq) (*RegisterResp, error)
}

func RegisterProviderRegisterServiceServer(s *grpc.Server, srv ProviderRegisterServiceServer) {
	s.RegisterService(&_ProviderRegisterService_serviceDesc, srv)
}

func _ProviderRegisterService_GetPublicKey_Handler(srv interface{}, ctx context.Context, dec func(interface{}) error, interceptor grpc.UnaryServerInterceptor) (interface{}, error) {
	in := new(Empty)
	if err := dec(in); err != nil {
		return nil, err
	}
	if interceptor == nil {
		return srv.(ProviderRegisterServiceServer).GetPublicKey(ctx, in)
	}
	info := &grpc.UnaryServerInfo{
		Server:     srv,
		FullMethod: "/provider.client.pb.ProviderRegisterService/GetPublicKey",
	}
	handler := func(ctx context.Context, req interface{}) (interface{}, error) {
		return srv.(ProviderRegisterServiceServer).GetPublicKey(ctx, req.(*Empty))
	}
	return interceptor(ctx, in, info, handler)
}

func _ProviderRegisterService_Register_Handler(srv interface{}, ctx context.Context, dec func(interface{}) error, interceptor grpc.UnaryServerInterceptor) (interface{}, error) {
	in := new(RegisterReq)
	if err := dec(in); err != nil {
		return nil, err
	}
	if interceptor == nil {
		return srv.(ProviderRegisterServiceServer).Register(ctx, in)
	}
	info := &grpc.UnaryServerInfo{
		Server:     srv,
		FullMethod: "/provider.client.pb.ProviderRegisterService/Register",
	}
	handler := func(ctx context.Context, req interface{}) (interface{}, error) {
		return srv.(ProviderRegisterServiceServer).Register(ctx, req.(*RegisterReq))
	}
	return interceptor(ctx, in, info, handler)
}

var _ProviderRegisterService_serviceDesc = grpc.ServiceDesc{
	ServiceName: "provider.client.pb.ProviderRegisterService",
	HandlerType: (*ProviderRegisterServiceServer)(nil),
	Methods: []grpc.MethodDesc{
		{
			MethodName: "GetPublicKey",
			Handler:    _ProviderRegisterService_GetPublicKey_Handler,
		},
		{
			MethodName: "Register",
			Handler:    _ProviderRegisterService_Register_Handler,
		},
	},
	Streams:  []grpc.StreamDesc{},
	Metadata: "provider_register.proto",
}

func init() { proto.RegisterFile("provider_register.proto", fileDescriptor0) }

var fileDescriptor0 = []byte{
	// 411 bytes of a gzipped FileDescriptorProto
	0x1f, 0x8b, 0x08, 0x00, 0x00, 0x00, 0x00, 0x00, 0x02, 0xff, 0x7c, 0x93, 0xdf, 0x6e, 0xd3, 0x30,
	0x14, 0x87, 0x97, 0x75, 0xeb, 0x9f, 0xd3, 0x0c, 0xc6, 0xb9, 0x60, 0x61, 0x42, 0x22, 0x44, 0x5c,
	0x44, 0x08, 0x72, 0x31, 0x9e, 0x00, 0xd8, 0x84, 0xd0, 0x24, 0x56, 0x79, 0x80, 0xc4, 0x15, 0x4a,
	0xe2, 0xa3, 0x61, 0xc9, 0x4d, 0x8c, 0xed, 0xb4, 0xca, 0x93, 0x21, 0xf1, 0x74, 0x28, 0xa6, 0xa1,
	0xf1, 0x5a, 0x71, 0x57, 0x7f, 0xe7, 0xe7, 0xaf, 0x47, 0x3e, 0x27, 0x70, 0xa6, 0x74, 0xbd, 0x12,
	0x9c, 0xf4, 0x77, 0x4d, 0x77, 0xc2, 0x58, 0xd2, 0x99, 0xd2, 0xb5, 0xad, 0x11, 0xfb, 0x42, 0x56,
	0x4a, 0x41, 0x95, 0xcd, 0x54, 0x91, 0x4c, 0xe0, 0xf8, 0x6a, 0xa9, 0x6c, 0x9b, 0xbc, 0x86, 0x93,
	0x45, 0x53, 0x48, 0x51, 0x5e, 0x53, 0xcb, 0xc8, 0x28, 0x7c, 0x0a, 0x33, 0xd5, 0x83, 0x28, 0x88,
	0x83, 0x34, 0x64, 0x5b, 0x90, 0xfc, 0x1a, 0xc1, 0x9c, 0x6d, 0xf4, 0x8c, 0x7e, 0xe2, 0x63, 0x18,
	0x57, 0x35, 0xa7, 0x8f, 0xdc, 0x45, 0x67, 0x6c, 0x73, 0xf2, 0x2d, 0x87, 0xf7, 0x2c, 0x78, 0x09,
	0x61, 0xd9, 0x68, 0x4d, 0x55, 0xd9, 0x7e, 0x6e, 0x15, 0x45, 0xa3, 0x38, 0x48, 0x1f, 0x5c, 0xc4,
	0xd9, 0x6e, 0xa3, 0xd9, 0xfb, 0x41, 0x8e, 0x79, 0xb7, 0xf0, 0x05, 0x9c, 0xac, 0x73, 0x29, 0xc9,
	0xbe, 0xe5, 0x5c, 0x93, 0x31, 0xd1, 0x91, 0x6b, 0xc1, 0x87, 0x5d, 0x27, 0x85, 0x90, 0xf2, 0x6a,
	0x99, 0x0b, 0x19, 0x1d, 0xbb, 0xc4, 0x16, 0x74, 0x0e, 0x63, 0x6b, 0x9d, 0xdf, 0xd1, 0xd7, 0x5a,
	0x36, 0x4b, 0x8a, 0xc6, 0x71, 0x90, 0x1e, 0x31, 0x1f, 0x62, 0x0c, 0xf3, 0x46, 0xbd, 0xcb, 0x2b,
	0xbe, 0x16, 0xdc, 0xfe, 0x88, 0x26, 0x2e, 0x33, 0x44, 0x9d, 0x87, 0xd7, 0xeb, 0x6a, 0x9b, 0x99,
	0xfe, 0xf5, 0x78, 0x10, 0x53, 0x78, 0x68, 0xc9, 0xd8, 0x2f, 0x03, 0xd7, 0xcc, 0xe5, 0xee, 0x63,
	0x7c, 0x05, 0x8f, 0x3a, 0x74, 0xe9, 0x39, 0xc1, 0x65, 0x77, 0x0b, 0x98, 0x40, 0x98, 0xaf, 0x72,
	0x21, 0xf3, 0x42, 0x48, 0x61, 0xdb, 0x68, 0x1e, 0x07, 0xe9, 0x21, 0xf3, 0x58, 0x92, 0x42, 0xb8,
	0x1d, 0x9c, 0x51, 0x18, 0xc1, 0xc4, 0x34, 0x65, 0xd9, 0xbd, 0x5b, 0x37, 0xba, 0x29, 0xeb, 0x8f,
	0x2f, 0x63, 0x08, 0x87, 0xaf, 0x8e, 0x13, 0x18, 0xdd, 0x2e, 0x6e, 0x4e, 0x0f, 0xdc, 0x8f, 0xeb,
	0x6f, 0xa7, 0xc1, 0xc5, 0xef, 0x00, 0xce, 0x16, 0x9b, 0x59, 0xf5, 0xd2, 0x5b, 0xd2, 0x2b, 0x51,
	0x12, 0x7e, 0x82, 0xf0, 0x03, 0xd9, 0x7f, 0x3b, 0x85, 0x4f, 0xf6, 0x4d, 0xd5, 0xed, 0xde, 0xf9,
	0xf3, 0x7d, 0x25, 0x6f, 0x1b, 0x93, 0x03, 0xbc, 0x81, 0x69, 0xff, 0x17, 0xf8, 0x6c, 0xdf, 0x85,
	0xc1, 0x3a, 0x9e, 0xc7, 0xff, 0x0f, 0x74, 0xc2, 0x62, 0xec, 0xbe, 0x8a, 0x37, 0x7f, 0x02, 0x00,
	0x00, 0xff, 0xff, 0x8c, 0x96, 0x69, 0xdf, 0x30, 0x03, 0x00, 0x00,
}