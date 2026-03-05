package service

type Middleware func(serviceHandler) serviceHandler
