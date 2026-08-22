package oscout

import (
	"net"
	"strconv"
)

type Sender struct {
	conn net.PacketConn
	dest net.Addr
	addr string
}

func New(host string, port int, paramAddr string) (*Sender, error) {
	dest, err := net.ResolveUDPAddr("udp", net.JoinHostPort(host, strconv.Itoa(port)))
	if err != nil {
		return nil, err
	}
	// 接続済みソケットだと受け手不在時のICMPで次の送信がエラーになるため非接続にする
	conn, err := net.ListenPacket("udp", ":0")
	if err != nil {
		return nil, err
	}
	return &Sender{conn: conn, dest: dest, addr: paramAddr}, nil
}

// boolはOSCタイプタグ(T/F)自体が値なのでペイロードはない
func (s *Sender) Send(isMaster bool) {
	tag := ",F"
	if isMaster {
		tag = ",T"
	}
	msg := append(pad([]byte(s.addr)), pad([]byte(tag))...)
	s.conn.WriteTo(msg, s.dest)
}

// NUL終端して4バイト境界までパディング
func pad(b []byte) []byte {
	b = append(b, 0)
	for len(b)%4 != 0 {
		b = append(b, 0)
	}
	return b
}
