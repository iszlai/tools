// Package grpcservice covers FR-16's gRPC service-method heuristic: an
// interface named "*Server" whose package also declares "Register*Server"
// (the protoc-gen-go-grpc generated-code pattern), plus the
// "Unimplemented*Server" embedding strong hint.
package grpcservice

// GreeterServer stands in for a generated gRPC service interface.
type GreeterServer interface {
	SayHello(name string) (string, error)
	mustEmbedUnimplementedGreeterServer()
}

// RegisterGreeterServer stands in for the generated registration func; its
// mere presence next to the GreeterServer interface is FR-16's signal that
// GreeterServer is a gRPC service contract, not just any interface.
func RegisterGreeterServer(srv GreeterServer) {}

// UnimplementedGreeterServer stands in for the generated forward-compatible
// base type.
type UnimplementedGreeterServer struct{}

func (UnimplementedGreeterServer) SayHello(name string) (string, error) { return "", nil }
func (UnimplementedGreeterServer) mustEmbedUnimplementedGreeterServer() {}

// server is a full implementation: embedding plus its own SayHello method
// makes it satisfy GreeterServer outright, so its SayHello is found by the
// direct types.Implements check.
type server struct {
	UnimplementedGreeterServer
}

func (s *server) SayHello(name string) (string, error) {
	return "hello " + name, nil
}

// partialServer embeds UnimplementedGreeterServer but overrides SayHello
// with an incompatible signature, so it does NOT actually satisfy
// GreeterServer (the override shadows the promoted method rather than
// matching it). FR-16 should still report its SayHello as a gRPC entry
// point from the embedding alone — the "strong hint" the slice's spec asks
// for.
type partialServer struct {
	UnimplementedGreeterServer
}

func (p *partialServer) SayHello(name string) string { return "hi " + name }
