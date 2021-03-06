package crypto

import (
	"crypto/rand"

	. "github.com/onsi/ginkgo"
	. "github.com/onsi/gomega"
)

var _ = Describe("Chacha20poly1305", func() {
	var (
		alice, bob AEAD
	)

	BeforeEach(func() {
		keyAlice := make([]byte, 32)
		keyBob := make([]byte, 32)
		ivAlice := make([]byte, 4)
		ivBob := make([]byte, 4)
		rand.Reader.Read(keyAlice)
		rand.Reader.Read(keyBob)
		rand.Reader.Read(ivAlice)
		rand.Reader.Read(ivBob)
		var err error
		alice, err = NewAEADChacha20Poly1305(keyBob, keyAlice, ivBob, ivAlice)
		Expect(err).ToNot(HaveOccurred())
		bob, err = NewAEADChacha20Poly1305(keyAlice, keyBob, ivAlice, ivBob)
		Expect(err).ToNot(HaveOccurred())
	})

	It("seals and opens", func() {
		b := alice.Seal(42, []byte("aad"), []byte("foobar"))
		text, err := bob.Open(42, []byte("aad"), b)
		Expect(err).ToNot(HaveOccurred())
		Expect(text).To(Equal([]byte("foobar")))
	})

	It("seals and opens reverse", func() {
		b := bob.Seal(42, []byte("aad"), []byte("foobar"))
		text, err := alice.Open(42, []byte("aad"), b)
		Expect(err).ToNot(HaveOccurred())
		Expect(text).To(Equal([]byte("foobar")))
	})

	It("fails with wrong aad", func() {
		b := alice.Seal(42, []byte("aad"), []byte("foobar"))
		_, err := bob.Open(42, []byte("aad2"), b)
		Expect(err).To(HaveOccurred())
	})
})
