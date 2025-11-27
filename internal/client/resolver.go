package client

import (
	"context"
	"fmt"
	"time"

	"github.com/miekg/dns"
)

func RaceResolve(ctx context.Context, req *dns.Msg, clients []DNSClient) (*dns.Msg, error) {
	if len(clients) == 0 {
		return nil, fmt.Errorf("没有可用的上游客户端")
	}

	raceCtx, cancel := context.WithCancel(ctx)
	defer cancel()

	results := make(chan *dns.Msg, 1)
	errs := make(chan error, len(clients))

	for _, c := range clients {
		reqClone := req.Copy()

		go func(cl DNSClient) {
			resp, err := cl.Resolve(raceCtx, reqClone)
			if err != nil {
				errs <- err
				return
			}
			select {
			case results <- resp:
			case <-raceCtx.Done():
			default:
			}
		}(c)
	}

	var lastErr error
	for i := 0; i < len(clients); i++ {
		select {
		case resp := <-results:
			return resp, nil
		case err := <-errs:
			lastErr = err
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-time.After(5 * time.Second):
			return nil, fmt.Errorf("并发查询超时")
		}
	}

	if lastErr != nil {
		return nil, fmt.Errorf("所有上游查询均失败: %w", lastErr)
	}
	return nil, fmt.Errorf("未知错误：未收到任何响应")
}
