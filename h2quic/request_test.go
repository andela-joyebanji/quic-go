package h2quic

import (
	"net/http"

	"golang.org/x/net/http2/hpack"

	. "github.com/onsi/ginkgo"
	. "github.com/onsi/gomega"
)

var _ = Describe("Request", func() {
	It("populates request", func() {
		headers := []hpack.HeaderField{
			{":path", "/foo", false},
			{":authority", "quic.clemente.io", false},
			{":method", "GET", false},
		}
		req, err := requestFromHeaders(headers)
		Expect(err).NotTo(HaveOccurred())
		Expect(req.Method).To(Equal("GET"))
		Expect(req.URL.Path).To(Equal("/foo"))
		Expect(req.Proto).To(Equal("HTTP/2.0"))
		Expect(req.ProtoMajor).To(Equal(2))
		Expect(req.ProtoMinor).To(Equal(0))
		Expect(req.Header).To(BeEmpty())
		Expect(req.Body).To(BeNil())
		Expect(req.Host).To(Equal("quic.clemente.io"))
		Expect(req.RequestURI).To(Equal("/foo"))
	})

	It("handles other headers", func() {
		headers := []hpack.HeaderField{
			{":path", "/foo", false},
			{":authority", "quic.clemente.io", false},
			{":method", "GET", false},
			{"content-length", "42", false},
			{"duplicate-header", "1", false},
			{"duplicate-header", "2", false},
		}
		req, err := requestFromHeaders(headers)
		Expect(err).NotTo(HaveOccurred())
		Expect(req.Header).To(Equal(http.Header{
			"Content-Length":   []string{"42"},
			"Duplicate-Header": []string{"1", "2"},
		}))
	})

	It("errors with missing path", func() {
		headers := []hpack.HeaderField{
			{":authority", "quic.clemente.io", false},
			{":method", "GET", false},
		}
		_, err := requestFromHeaders(headers)
		Expect(err).To(MatchError(":path, :authority and :method must not be empty"))
	})

	It("errors with missing method", func() {
		headers := []hpack.HeaderField{
			{":path", "/foo", false},
			{":authority", "quic.clemente.io", false},
		}
		_, err := requestFromHeaders(headers)
		Expect(err).To(MatchError(":path, :authority and :method must not be empty"))
	})

	It("errors with missing authority", func() {
		headers := []hpack.HeaderField{
			{":path", "/foo", false},
			{":method", "GET", false},
		}
		_, err := requestFromHeaders(headers)
		Expect(err).To(MatchError(":path, :authority and :method must not be empty"))
	})
})
