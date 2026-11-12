package bricks

// WebSocket 客户端（RFC6455 最小实现）——CDP 传输层。
//
// 只覆盖 CDP 所需子集：客户端掩码文本帧 / 分片累积 / ping-pong / close。
// 零第三方依赖（对齐 C# ClientWebSocket 的零依赖口径）。

import (
	"bufio"
	"bytes"
	"crypto/rand"
	"crypto/sha1"
	"encoding/base64"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"net"
	"net/url"
	"strings"
	"time"
)

// wsMaxMessageBytes 是单条消息上限——防失控分配（CDP 截图 base64 可达数 MB）。
const wsMaxMessageBytes = 64 << 20

// wsAcceptGUID 是 WebSocket 握手魔术串（RFC6455 §4.2.2）。
const wsAcceptGUID = "258EAFA5-E914-47DA-95CA-C5AB0DC85B11"

// wsConn 是极简 WebSocket 客户端连接（文本帧）。
type wsConn struct {
	// conn 底层 TCP 连接
	conn net.Conn
	// reader 带缓冲读（握手后保留残余字节）
	reader *bufio.Reader
	// closed 关闭标记（IsAlive 口径：未显式关闭即视为可复用——与 C# WebSocketState.Open 同义）
	closed bool
}

// wsDial 建立 WebSocket 连接——握手校验 101 + Sec-WebSocket-Accept。
func wsDial(rawURL string, timeout time.Duration) (*wsConn, error) {
	parsed, err := url.Parse(rawURL)
	if err != nil {
		return nil, err
	}
	if parsed.Scheme != "ws" {
		return nil, fmt.Errorf("仅支持 ws 协议: %s", parsed.Scheme)
	}
	address := parsed.Host
	if !strings.Contains(address, ":") {
		address += ":80"
	}
	conn, err := net.DialTimeout("tcp", address, timeout)
	if err != nil {
		return nil, err
	}
	keyRaw := make([]byte, 16)
	if _, err := rand.Read(keyRaw); err != nil {
		_ = conn.Close()
		return nil, err
	}
	key := base64.StdEncoding.EncodeToString(keyRaw)
	request := "GET " + parsed.RequestURI() + " HTTP/1.1\r\n" +
		"Host: " + parsed.Host + "\r\n" +
		"Upgrade: websocket\r\n" +
		"Connection: Upgrade\r\n" +
		"Sec-WebSocket-Key: " + key + "\r\n" +
		"Sec-WebSocket-Version: 13\r\n\r\n"
	_ = conn.SetWriteDeadline(time.Now().Add(timeout))
	if _, err := conn.Write([]byte(request)); err != nil {
		_ = conn.Close()
		return nil, err
	}
	reader := bufio.NewReaderSize(conn, 1<<16)
	_ = conn.SetReadDeadline(time.Now().Add(timeout))
	statusLine, err := wsReadLine(reader)
	if err != nil {
		_ = conn.Close()
		return nil, err
	}
	if !strings.Contains(statusLine, " 101") {
		_ = conn.Close()
		return nil, fmt.Errorf("WebSocket 握手失败: %s", statusLine)
	}
	accept := ""
	for {
		line, err := wsReadLine(reader)
		if err != nil {
			_ = conn.Close()
			return nil, err
		}
		if line == "" {
			break
		}
		if idx := strings.IndexByte(line, ':'); idx > 0 {
			name := strings.ToLower(strings.TrimSpace(line[:idx]))
			if name == "sec-websocket-accept" {
				accept = strings.TrimSpace(line[idx+1:])
			}
		}
	}
	expected := wsAcceptKey(key)
	if accept != expected {
		_ = conn.Close()
		return nil, errors.New("WebSocket 握手校验失败（Sec-WebSocket-Accept 不符）")
	}
	return &wsConn{conn: conn, reader: reader}, nil
}

// wsAcceptKey 计算期望的 Sec-WebSocket-Accept。
func wsAcceptKey(key string) string {
	sum := sha1.Sum([]byte(key + wsAcceptGUID))
	return base64.StdEncoding.EncodeToString(sum[:])
}

// wsReadLine 读一行（去行尾）。
func wsReadLine(reader *bufio.Reader) (string, error) {
	line, err := reader.ReadString('\n')
	if err != nil {
		return "", err
	}
	return strings.TrimRight(line, "\r\n"), nil
}

// WriteText 发送一条文本消息（客户端帧必须掩码）。
func (c *wsConn) WriteText(payload []byte, timeout time.Duration) error {
	return c.writeFrame(0x1, payload, timeout)
}

// writeFrame 发送单帧（FIN=1）。
func (c *wsConn) writeFrame(opcode byte, payload []byte, timeout time.Duration) error {
	frame := make([]byte, 0, 14+len(payload))
	frame = append(frame, 0x80|opcode)
	length := len(payload)
	switch {
	case length <= 125:
		frame = append(frame, byte(length)|0x80)
	case length <= 0xFFFF:
		frame = append(frame, 126|0x80, byte(length>>8), byte(length))
	default:
		frame = append(frame, 127|0x80)
		var ext [8]byte
		binary.BigEndian.PutUint64(ext[:], uint64(length))
		frame = append(frame, ext[:]...)
	}
	mask := make([]byte, 4)
	if _, err := rand.Read(mask); err != nil {
		return err
	}
	frame = append(frame, mask...)
	for i := 0; i < len(payload); i++ {
		frame = append(frame, payload[i]^mask[i%4])
	}
	if timeout > 0 {
		_ = c.conn.SetWriteDeadline(time.Now().Add(timeout))
	}
	_, err := c.conn.Write(frame)
	return err
}

// ReadMessage 读一条完整文本消息（分片累积；ping 自动回 pong；close 返回 io.EOF）。
func (c *wsConn) ReadMessage(deadline time.Time) (string, error) {
	if err := c.conn.SetReadDeadline(deadline); err != nil {
		return "", err
	}
	var out bytes.Buffer
	for {
		var header [2]byte
		if _, err := io.ReadFull(c.reader, header[:]); err != nil {
			return "", err
		}
		fin := header[0]&0x80 != 0
		opcode := header[0] & 0x0F
		masked := header[1]&0x80 != 0
		length := int64(header[1] & 0x7F)
		switch length {
		case 126:
			var ext [2]byte
			if _, err := io.ReadFull(c.reader, ext[:]); err != nil {
				return "", err
			}
			length = int64(binary.BigEndian.Uint16(ext[:]))
		case 127:
			var ext [8]byte
			if _, err := io.ReadFull(c.reader, ext[:]); err != nil {
				return "", err
			}
			length = int64(binary.BigEndian.Uint64(ext[:]))
		}
		if length < 0 || length > wsMaxMessageBytes {
			return "", errors.New("WebSocket 帧超长")
		}
		var maskKey [4]byte
		if masked {
			if _, err := io.ReadFull(c.reader, maskKey[:]); err != nil {
				return "", err
			}
		}
		payload := make([]byte, length)
		if length > 0 {
			if _, err := io.ReadFull(c.reader, payload); err != nil {
				return "", err
			}
		}
		if masked {
			for i := range payload {
				payload[i] ^= maskKey[i%4]
			}
		}
		switch opcode {
		case 0x0, 0x1, 0x2:
			// continuation / text / binary——累积到消息完整
			out.Write(payload)
			if fin {
				return out.String(), nil
			}
		case 0x8:
			// close——对端关闭
			return "", io.EOF
		case 0x9:
			// ping——回 pong（控制帧 ≤125 字节）
			_ = c.writeFrame(0xA, payload, 5*time.Second)
		case 0xA:
			// pong——忽略
		default:
			// 保留 opcode——忽略
		}
	}
}

// Close 关闭连接。
func (c *wsConn) Close() error {
	c.closed = true
	return c.conn.Close()
}

// isOpen 连接可用性（未显式关闭即视为可复用——C# WebSocketState.Open 同义）。
func (c *wsConn) isOpen() bool {
	return c != nil && !c.closed
}

// wsTimeout 报告错误是否为超时（net.Error.Timeout）。
func wsTimeout(err error) bool {
	var netErr net.Error
	if errors.As(err, &netErr) {
		return netErr.Timeout()
	}
	return false
}
