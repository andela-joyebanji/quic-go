package handshake

import (
	"bytes"

	"github.com/lucas-clemente/quic-go/crypto"

	. "github.com/onsi/ginkgo"
	. "github.com/onsi/gomega"
)

var _ = Describe("ServerConfig", func() {
	var (
		kex  crypto.KeyExchange
		scfg *ServerConfig
	)

	BeforeEach(func() {
		var err error
		kex, err = crypto.NewCurve25519KEX()
		Expect(err).NotTo(HaveOccurred())
		scfg, err = NewServerConfig(kex, nil)
		Expect(err).NotTo(HaveOccurred())
	})

	It("gets the proper binary representation", func() {
		expected := bytes.NewBuffer([]byte{0x53, 0x43, 0x46, 0x47, 0x7, 0x0, 0x0, 0x0, 0x56, 0x45, 0x52, 0x0, 0x4, 0x0, 0x0, 0x0, 0x41, 0x45, 0x41, 0x44, 0x8, 0x0, 0x0, 0x0, 0x53, 0x43, 0x49, 0x44, 0x18, 0x0, 0x0, 0x0, 0x50, 0x55, 0x42, 0x53, 0x3b, 0x0, 0x0, 0x0, 0x4b, 0x45, 0x58, 0x53, 0x3f, 0x0, 0x0, 0x0, 0x4f, 0x42, 0x49, 0x54, 0x47, 0x0, 0x0, 0x0, 0x45, 0x58, 0x50, 0x59, 0x4f, 0x0, 0x0, 0x0, 0x51, 0x30, 0x33, 0x32, 0x43, 0x43, 0x32, 0x30})
		expected.Write(scfg.ID)
		expected.Write([]byte{0x20, 0x0, 0x0})
		expected.Write(kex.PublicKey())
		expected.Write([]byte{0x43, 0x32, 0x35, 0x35, 0x0, 0x1, 0x2, 0x3, 0x4, 0x5, 0x6, 0x7, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff})
		Expect(scfg.Get()).To(Equal(expected.Bytes()))
	})
})
