package oscout

import (
	"net"
	"strconv"
)

type Sender struct {
	conn net.Conn
	addr string
}

func New(host string, port int, paramAddr string) (*Sender, error) {
	conn, err := net.Dial("udp", net.JoinHostPort(host, strconv.Itoa(port)))
	if err != nil {
		return nil, err
	}
	return &Sender{conn: conn, addr: paramAddr}, nil
}

// boolはOSCタイプタグ(T/F)自体が値なのでペイロードはない
func (s *Sender) Send(isMaster bool) error {
	tag := ",F"
	if isMaster {
		tag = ",T"
	}
	msg := append(pad([]byte(s.addr)), pad([]byte(tag))...)
	_, err := s.conn.Write(msg)
	return err
}

// NUL終端して4バイト境界までパディング
func pad(b []byte) []byte {
	b = append(b, 0)
	for len(b)%4 != 0 {
		b = append(b, 0)
	}
	return b
}
