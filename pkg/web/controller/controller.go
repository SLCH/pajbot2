package controller

import (
	"github.com/pajlada/pajbot2/pkg"
	"github.com/pajlada/pajbot2/pkg/common/config"
	"github.com/pajlada/pajbot2/pkg/web/controller/admin"
	"github.com/pajlada/pajbot2/pkg/web/controller/api"
	"github.com/pajlada/pajbot2/pkg/web/controller/banphrases"
	"github.com/pajlada/pajbot2/pkg/web/controller/dashboard"
	"github.com/pajlada/pajbot2/pkg/web/controller/home"
	"github.com/pajlada/pajbot2/pkg/web/controller/logout"
	"github.com/pajlada/pajbot2/pkg/web/controller/static"
	"github.com/pajlada/pajbot2/pkg/web/controller/ws"
	"github.com/pajlada/pajbot2/pkg/web/router"
)

func LoadRoutes(a pkg.Application, cfg *config.Config) {
	dashboard.Load()
	home.Load()
	api.Load(a, cfg)
	static.Load()
	ws.Load()

	// /logout
	logout.Load()

	// /profile
	router.Get("/profile", handleProfile)

	// /banphrases
	banphrases.Load()

	// /admin
	admin.Load(a)
}
