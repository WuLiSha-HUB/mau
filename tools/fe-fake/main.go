// Command fe-fake 是前端测试上游的替身——对拍 /api/v1/frontend-test 转发时提供固定响应。
//
// 为什么不用真实 node server.js 做对拍上游：Vitest 每次输出含耗时与执行序，body 不可
// 逐字节比；而转发保真的判据是「宿主 body == 上游 body」，要求上游响应可控。
// 正向拉起（真实 server.js）另由宿主的 fetest 附件自证，见 CHANGELOG。
package main

import (
	"flag"
	"fmt"
	"net/http"
)

func main() {
	port := flag.Int("port", 8069, "监听端口")
	body := flag.String("body", `{"ok":true,"code":0,"stdout":"fe-fake","stderr":""}`, "GET /api/test/unit 的固定响应 body")
	flag.Parse()

	mux := http.NewServeMux()
	mux.HandleFunc("/api/test/unit", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(*body))
	})
	mux.HandleFunc("/health", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = fmt.Fprintf(w, `{"ok":true,"service":"fe-fake","port":%d}`, *port)
	})
	addr := fmt.Sprintf("127.0.0.1:%d", *port)
	fmt.Println("fe-fake listening", addr)
	if err := http.ListenAndServe(addr, mux); err != nil {
		fmt.Println("fe-fake 退出:", err)
	}
}
