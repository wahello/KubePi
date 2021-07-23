package proxy

import (
	"encoding/pem"
	"fmt"
	"github.com/KubeOperator/ekko/internal/api/v1/session"
	"github.com/KubeOperator/ekko/internal/service/v1/cluster"
	"github.com/KubeOperator/ekko/internal/service/v1/clusterbinding"
	"github.com/KubeOperator/ekko/internal/service/v1/common"
	"github.com/kataras/iris/v12"
	"github.com/kataras/iris/v12/context"
	"k8s.io/client-go/rest"
	"net/http"
	"net/http/httputil"
	"net/url"
)

type Handler struct {
	clusterService        cluster.Service
	clusterBindingService clusterbinding.Service
}

func NewHandler() *Handler {
	return &Handler{
		clusterService:        cluster.NewService(),
		clusterBindingService: clusterbinding.NewService(),
	}
}

func (h *Handler) KubernetesAPIProxy() iris.Handler {
	return func(ctx *context.Context) {
		// 查询当前集群
		name := ctx.Params().GetString("name")
		proxyPath := ctx.Params().GetString("p")
		c, err := h.clusterService.Get(name, common.DBOptions{})
		if err != nil {
			ctx.StatusCode(iris.StatusInternalServerError)
			ctx.Values().Set("message", fmt.Sprintf("get cluster failed: %s", err.Error()))
			return
		}
		u := ctx.Values().Get("profile")
		profile := u.(session.UserProfile)
		binding, err := h.clusterBindingService.GetBindingByClusterNameAndUserName(name, profile.Name, common.DBOptions{})
		if err != nil {
			ctx.StatusCode(iris.StatusForbidden)
			ctx.Values().Set("message", fmt.Sprintf("user %s not cluster %s member ", profile.Name, name))
			return
		}
		kubeConf := &rest.Config{
			Host: c.Spec.Connect.Forward.ApiServer,
			TLSClientConfig: rest.TLSClientConfig{
				Insecure: true,
				CertData: binding.Certificate,
				KeyData:  pem.EncodeToMemory(&pem.Block{Type: "RSA PRIVATE KEY", Bytes: c.PrivateKey}),
			},
		}
		apiUrl, err := url.Parse(c.Spec.Connect.Forward.ApiServer)
		if err != nil {
			ctx.StatusCode(iris.StatusInternalServerError)
			ctx.Values().Set("message", fmt.Sprintf("parse cluster api server failed: %s", err.Error()))
			return
		}
		ts, err := rest.TransportFor(kubeConf)
		if err != nil {
			ctx.StatusCode(iris.StatusInternalServerError)
			ctx.Values().Set("message", fmt.Sprintf("parse cluster api server failed: %s", err.Error()))
			return
		}
		reverseProxy := httputil.NewSingleHostReverseProxy(apiUrl)
		reverseProxy.Transport = ts
		ctx.Request().URL.Path = proxyPath
		if ctx.Method() == "PATCH" {
			ctx.Request().Header.Set("Content-Type", "application/merge-patch+json")
		}
		reverseProxy.ModifyResponse = func(response *http.Response) error {
			if response.StatusCode == http.StatusForbidden {
				response.StatusCode = http.StatusInternalServerError
			}
			return nil
		}
		reverseProxy.ServeHTTP(ctx.ResponseWriter(), ctx.Request())
	}
}

func Install(parent iris.Party) {
	handler := NewHandler()
	sp := parent.Party("/proxy")
	sp.Any("/:name/k8s/{p:path}", handler.KubernetesAPIProxy())
}
