package api

// DTO 由后端的 OpenAPI 生成（设计稿 §10.7 第 3 条：CLI ↔ spec 契约）。oapi-codegen 用 `go run …@版本` 调起，
// 不进 go.mod —— 它的依赖树很大，只在生成时需要。
//
//go:generate go run github.com/oapi-codegen/oapi-codegen/v2/cmd/oapi-codegen@v2.8.0 -config oapi-codegen.yaml ../../../backend/api/openapi.yaml
