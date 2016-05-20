package quic

import (
	"bytes"
	"crypto/tls"
	"net"
	"sync"

	"github.com/lucas-clemente/quic-go/crypto"
	"github.com/lucas-clemente/quic-go/handshake"
	"github.com/lucas-clemente/quic-go/protocol"
	"github.com/lucas-clemente/quic-go/qerr"
	"github.com/lucas-clemente/quic-go/utils"
)

// packetHandler handles packets
type packetHandler interface {
	handlePacket(addr interface{}, hdr *publicHeader, data []byte)
	run()
}

type packetToSend struct {
	addr *net.UDPAddr
	p    []byte
}

// A Server of QUIC
type Server struct {
	conns      []*net.UDPConn
	connsMutex sync.Mutex

	signer crypto.Signer
	scfg   *handshake.ServerConfig

	sessions      map[protocol.ConnectionID]packetHandler
	sessionsMutex sync.RWMutex

	streamCallback StreamCallback

	newSession func(conn connection, v protocol.VersionNumber, connectionID protocol.ConnectionID, sCfg *handshake.ServerConfig, streamCallback StreamCallback, closeCallback closeCallback) (packetHandler, error)

	packetsToSend chan packetToSend
}

// NewServer makes a new server
func NewServer(tlsConfig *tls.Config, cb StreamCallback) (*Server, error) {
	signer, err := crypto.NewRSASigner(tlsConfig)
	if err != nil {
		return nil, err
	}

	kex, err := crypto.NewCurve25519KEX()
	if err != nil {
		return nil, err
	}
	scfg, err := handshake.NewServerConfig(kex, signer)
	if err != nil {
		return nil, err
	}

	return &Server{
		signer:         signer,
		scfg:           scfg,
		streamCallback: cb,
		sessions:       map[protocol.ConnectionID]packetHandler{},
		newSession:     newSession,
		packetsToSend:  make(chan packetToSend, 128),
	}, nil
}

// ListenAndServe listens and serves a connection
func (s *Server) ListenAndServe(address string) error {
	addr, err := net.ResolveUDPAddr("udp", address)
	if err != nil {
		return err
	}

	conn, err := net.ListenUDP("udp", addr)
	if err != nil {
		return err
	}
	s.connsMutex.Lock()
	s.conns = append(s.conns, conn)
	s.connsMutex.Unlock()

	go func() {
		for p := range s.packetsToSend {
			conn.WriteToUDP(p.p, p.addr)
		}
	}()

	for {
		data := make([]byte, protocol.MaxPacketSize)
		n, remoteAddr, err := conn.ReadFromUDP(data)
		if err != nil {
			return err
		}
		data = data[:n]
		if err := s.handlePacket(conn, remoteAddr, data); err != nil {
			utils.Errorf("error handling packet: %s", err.Error())
		}
	}
}

// Close the server
func (s *Server) Close() error {
	s.connsMutex.Lock()
	defer s.connsMutex.Unlock()
	close(s.packetsToSend)
	for _, c := range s.conns {
		err := c.Close()
		if err != nil {
			return err
		}
	}
	return nil
}

func (s *Server) handlePacket(conn *net.UDPConn, remoteAddr *net.UDPAddr, packet []byte) error {
	if protocol.ByteCount(len(packet)) > protocol.MaxPacketSize {
		return qerr.PacketTooLarge
	}

	r := bytes.NewReader(packet)

	hdr, err := parsePublicHeader(r)
	if err != nil {
		return qerr.Error(qerr.InvalidPacketHeader, err.Error())
	}
	hdr.Raw = packet[:len(packet)-r.Len()]

	// Send Version Negotiation Packet if the client is speaking a different protocol version
	if hdr.VersionFlag && !protocol.IsSupportedVersion(hdr.VersionNumber) {
		utils.Infof("Client offered version %d, sending VersionNegotiationPacket", hdr.VersionNumber)
		_, err = conn.WriteToUDP(composeVersionNegotiation(hdr.ConnectionID), remoteAddr)
		if err != nil {
			return err
		}
		return nil
	}

	s.sessionsMutex.RLock()
	session, ok := s.sessions[hdr.ConnectionID]
	s.sessionsMutex.RUnlock()

	if !ok {
		utils.Infof("Serving new connection: %x, version %d from %v", hdr.ConnectionID, hdr.VersionNumber, remoteAddr)
		session, err = s.newSession(
			&udpConn{conn: conn, currentAddr: remoteAddr, server: s},
			hdr.VersionNumber,
			hdr.ConnectionID,
			s.scfg,
			s.streamCallback,
			s.closeCallback,
		)
		if err != nil {
			return err
		}
		go session.run()
		s.sessionsMutex.Lock()
		s.sessions[hdr.ConnectionID] = session
		s.sessionsMutex.Unlock()
	}
	if session == nil {
		// Late packet for closed session
		return nil
	}
	session.handlePacket(remoteAddr, hdr, packet[len(packet)-r.Len():])
	return nil
}

func (s *Server) closeCallback(id protocol.ConnectionID) {
	s.sessionsMutex.Lock()
	s.sessions[id] = nil
	s.sessionsMutex.Unlock()
}

func composeVersionNegotiation(connectionID protocol.ConnectionID) []byte {
	fullReply := &bytes.Buffer{}
	responsePublicHeader := publicHeader{
		ConnectionID: connectionID,
		PacketNumber: 1,
		VersionFlag:  true,
	}
	err := responsePublicHeader.WritePublicHeader(fullReply)
	if err != nil {
		utils.Errorf("error composing version negotiation packet: %s", err.Error())
	}
	fullReply.Write(protocol.SupportedVersionsAsTags)
	return fullReply.Bytes()
}
