package main

import (
	"log"
	"os"

	gcontext "github.com/golaravel/golaravel/pkg/context"
	"github.com/golaravel/golaravel/pkg/golaravel"
)

type BenchUserResponse struct {
	ID       int    `json:"id"`
	Name     string `json:"name"`
	Email    string `json:"email"`
	Role     string `json:"role"`
	IsActive bool   `json:"is_active"`
}

type BenchPostResponse struct {
	UserID string `json:"user_id"`
	PostID string `json:"post_id"`
	Status string `json:"status"`
}

func main() {
	port := os.Getenv("PORT")
	if port == "" {
		port = ":9001"
	}

	app := golaravel.New()

	app.GET("/api/user", func(c *gcontext.Context) error {
		return c.JSON(200, BenchUserResponse{
			ID:       101,
			Name:     "Taylor Otwell",
			Email:    "taylor@laravel.com",
			Role:     "Administrator",
			IsActive: true,
		})
	})

	app.GET("/api/users/:id/posts/:post_id", func(c *gcontext.Context) error {
		return c.JSON(200, BenchPostResponse{
			UserID: c.Param("id"),
			PostID: c.Param("post_id"),
			Status: "published",
		})
	})

	log.Printf("GoLaravel benchmark server listening on %s", port)
	if err := app.Run(port); err != nil {
		log.Fatal(err)
	}
}
