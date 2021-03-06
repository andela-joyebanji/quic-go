package handshake

import (
	"bytes"
	"crypto/rand"
	"io"
	"net"
	"sync"

	"github.com/lucas-clemente/quic-go/crypto"
	"github.com/lucas-clemente/quic-go/protocol"
	"github.com/lucas-clemente/quic-go/qerr"
	"github.com/lucas-clemente/quic-go/utils"
)

// KeyDerivationFunction is used for key derivation
type KeyDerivationFunction func(version protocol.VersionNumber, forwardSecure bool, sharedSecret, nonces []byte, connID protocol.ConnectionID, chlo []byte, scfg []byte, cert []byte, divNonce []byte) (crypto.AEAD, error)

// KeyExchangeFunction is used to make a new KEX
type KeyExchangeFunction func() (crypto.KeyExchange, error)

// The CryptoSetup handles all things crypto for the Session
type CryptoSetup struct {
	connID               protocol.ConnectionID
	ip                   net.IP
	version              protocol.VersionNumber
	scfg                 *ServerConfig
	nonce                []byte
	diversificationNonce []byte

	secureAEAD                  crypto.AEAD
	forwardSecureAEAD           crypto.AEAD
	receivedForwardSecurePacket bool
	receivedSecurePacket        bool
	aeadChanged                 chan struct{}

	keyDerivation KeyDerivationFunction
	keyExchange   KeyExchangeFunction

	cryptoStream utils.Stream

	connectionParametersManager *ConnectionParametersManager

	mutex sync.RWMutex
}

var _ crypto.AEAD = &CryptoSetup{}

// NewCryptoSetup creates a new CryptoSetup instance
func NewCryptoSetup(
	connID protocol.ConnectionID,
	ip net.IP,
	version protocol.VersionNumber,
	scfg *ServerConfig,
	cryptoStream utils.Stream,
	connectionParametersManager *ConnectionParametersManager,
	aeadChanged chan struct{},
) (*CryptoSetup, error) {
	nonce := make([]byte, 32)
	if _, err := io.ReadFull(rand.Reader, nonce); err != nil {
		return nil, err
	}
	diversificationNonce := make([]byte, 32)
	if _, err := io.ReadFull(rand.Reader, diversificationNonce); err != nil {
		return nil, err
	}
	return &CryptoSetup{
		connID:                      connID,
		ip:                          ip,
		version:                     version,
		scfg:                        scfg,
		nonce:                       nonce,
		diversificationNonce:        diversificationNonce,
		keyDerivation:               crypto.DeriveKeysChacha20,
		keyExchange:                 crypto.NewCurve25519KEX,
		cryptoStream:                cryptoStream,
		connectionParametersManager: connectionParametersManager,
		aeadChanged:                 aeadChanged,
	}, nil
}

// HandleCryptoStream reads and writes messages on the crypto stream
func (h *CryptoSetup) HandleCryptoStream() error {
	for {
		cachingReader := utils.NewCachingReader(h.cryptoStream)
		messageTag, cryptoData, err := ParseHandshakeMessage(cachingReader)
		if err != nil {
			return err
		}
		if messageTag != TagCHLO {
			return qerr.InvalidCryptoMessageType
		}
		chloData := cachingReader.Get()

		utils.Infof("Got CHLO:\n%s", printHandshakeMessage(cryptoData))

		done, err := h.handleMessage(chloData, cryptoData)
		if err != nil {
			return err
		}
		if done {
			return nil
		}
	}
}

func (h *CryptoSetup) handleMessage(chloData []byte, cryptoData map[Tag][]byte) (bool, error) {
	sniSlice, ok := cryptoData[TagSNI]
	if !ok {
		return false, qerr.Error(qerr.CryptoMessageParameterNotFound, "SNI required")
	}
	sni := string(sniSlice)
	if sni == "" {
		return false, qerr.Error(qerr.CryptoMessageParameterNotFound, "SNI required")
	}

	var reply []byte
	var err error
	if !h.isInchoateCHLO(cryptoData) {
		// We have a CHLO with a proper server config ID, do a 0-RTT handshake
		reply, err = h.handleCHLO(sni, chloData, cryptoData)
		if err != nil {
			return false, err
		}
		_, err = h.cryptoStream.Write(reply)
		if err != nil {
			return false, err
		}
		return true, nil
	}

	// We have an inchoate or non-matching CHLO, we now send a rejection
	reply, err = h.handleInchoateCHLO(sni, chloData, cryptoData)
	if err != nil {
		return false, err
	}
	_, err = h.cryptoStream.Write(reply)
	if err != nil {
		return false, err
	}
	return false, nil
}

// Open a message
func (h *CryptoSetup) Open(packetNumber protocol.PacketNumber, associatedData []byte, ciphertext []byte) ([]byte, error) {
	h.mutex.RLock()
	defer h.mutex.RUnlock()

	if h.forwardSecureAEAD != nil {
		res, err := h.forwardSecureAEAD.Open(packetNumber, associatedData, ciphertext)
		if err == nil {
			h.receivedForwardSecurePacket = true
			return res, nil
		}
		if h.receivedForwardSecurePacket {
			return nil, err
		}
	}
	if h.secureAEAD != nil {
		res, err := h.secureAEAD.Open(packetNumber, associatedData, ciphertext)
		if err == nil {
			h.receivedSecurePacket = true
			return res, nil
		}
		if h.receivedSecurePacket {
			return nil, err
		}
	}
	return (&crypto.NullAEAD{}).Open(packetNumber, associatedData, ciphertext)
}

// Seal a message
func (h *CryptoSetup) Seal(packetNumber protocol.PacketNumber, associatedData []byte, plaintext []byte) []byte {
	h.mutex.RLock()
	defer h.mutex.RUnlock()

	if h.receivedForwardSecurePacket {
		return h.forwardSecureAEAD.Seal(packetNumber, associatedData, plaintext)
	} else if h.secureAEAD != nil {
		return h.secureAEAD.Seal(packetNumber, associatedData, plaintext)
	} else {
		return (&crypto.NullAEAD{}).Seal(packetNumber, associatedData, plaintext)
	}
}

func (h *CryptoSetup) isInchoateCHLO(cryptoData map[Tag][]byte) bool {
	scid, ok := cryptoData[TagSCID]
	if !ok || !bytes.Equal(h.scfg.ID, scid) {
		return true
	}
	if err := h.scfg.stkSource.VerifyToken(h.ip, cryptoData[TagSTK]); err != nil {
		utils.Infof("STK invalid: %s", err.Error())
		return false
	}
	return false
}

func (h *CryptoSetup) handleInchoateCHLO(sni string, data []byte, cryptoData map[Tag][]byte) ([]byte, error) {
	if len(data) < protocol.ClientHelloMinimumSize {
		return nil, qerr.Error(qerr.CryptoInvalidValueLength, "CHLO too small")
	}

	var chloOrNil []byte
	if h.version > protocol.VersionNumber(30) {
		chloOrNil = data
	}

	proof, err := h.scfg.Sign(sni, chloOrNil)
	if err != nil {
		return nil, err
	}

	commonSetHashes := cryptoData[TagCCS]
	cachedCertsHashes := cryptoData[TagCCRT]

	certCompressed, err := h.scfg.GetCertsCompressed(sni, commonSetHashes, cachedCertsHashes)
	if err != nil {
		return nil, err
	}

	token, err := h.scfg.stkSource.NewToken(h.ip)
	if err != nil {
		return nil, err
	}

	var serverReply bytes.Buffer
	WriteHandshakeMessage(&serverReply, TagREJ, map[Tag][]byte{
		TagSCFG: h.scfg.Get(),
		TagCERT: certCompressed,
		TagPROF: proof,
		TagSTK:  token,
	})
	return serverReply.Bytes(), nil
}

func (h *CryptoSetup) handleCHLO(sni string, data []byte, cryptoData map[Tag][]byte) ([]byte, error) {
	// We have a CHLO matching our server config, we can continue with the 0-RTT handshake
	sharedSecret, err := h.scfg.kex.CalculateSharedKey(cryptoData[TagPUBS])
	if err != nil {
		return nil, err
	}

	h.mutex.Lock()
	defer h.mutex.Unlock()

	certUncompressed, err := h.scfg.signer.GetLeafCert(sni)
	if err != nil {
		return nil, err
	}

	h.secureAEAD, err = h.keyDerivation(
		h.version,
		false,
		sharedSecret,
		cryptoData[TagNONC],
		h.connID,
		data,
		h.scfg.Get(),
		certUncompressed,
		h.diversificationNonce,
	)
	if err != nil {
		return nil, err
	}

	// Generate a new curve instance to derive the forward secure key
	var fsNonce bytes.Buffer
	fsNonce.Write(cryptoData[TagNONC])
	fsNonce.Write(h.nonce)
	ephermalKex, err := h.keyExchange()
	if err != nil {
		return nil, err
	}
	ephermalSharedSecret, err := ephermalKex.CalculateSharedKey(cryptoData[TagPUBS])
	if err != nil {
		return nil, err
	}
	h.forwardSecureAEAD, err = h.keyDerivation(h.version,
		true,
		ephermalSharedSecret,
		fsNonce.Bytes(),
		h.connID,
		data,
		h.scfg.Get(),
		certUncompressed,
		nil,
	)
	if err != nil {
		return nil, err
	}

	err = h.connectionParametersManager.SetFromMap(cryptoData)
	if err != nil {
		return nil, err
	}

	replyMap := h.connectionParametersManager.GetSHLOMap()
	// add crypto parameters
	replyMap[TagPUBS] = ephermalKex.PublicKey()
	replyMap[TagSNO] = h.nonce
	replyMap[TagVER] = protocol.SupportedVersionsAsTags

	var reply bytes.Buffer
	WriteHandshakeMessage(&reply, TagSHLO, replyMap)

	h.aeadChanged <- struct{}{}

	return reply.Bytes(), nil
}

// DiversificationNonce returns a diversification nonce if required in the next packet to be Seal'ed
func (h *CryptoSetup) DiversificationNonce() []byte {
	if h.version < protocol.VersionNumber(33) {
		return nil
	}
	if h.receivedForwardSecurePacket || h.secureAEAD == nil {
		return nil
	}
	return h.diversificationNonce
}

func (h *CryptoSetup) verifyOrCreateSTK(token []byte) ([]byte, error) {
	err := h.scfg.stkSource.VerifyToken(h.ip, token)
	if err != nil {
		return h.scfg.stkSource.NewToken(h.ip)
	}
	return token, nil
}
