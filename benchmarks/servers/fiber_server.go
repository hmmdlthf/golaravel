package main

import (
	"log"
	"os"

	"github.com/gofiber/fiber/v2"
)

type FiberBenchUserResponse struct {
	ID       int    `json:"id"`
	Name     string `json:"name"`
	Email    string `json:"email"`
	Role     string `json:"role"`
	IsActive bool   `json:"is_active"`
}

type FiberBenchPostResponse struct {
	UserID string `json:"user_id"`
	PostID string `json:"post_id"`
	Status string `json:"status"`
}

func main() {
	port := os.Getenv("PORT")
	if port == "" {
		port = ":9003"
	}

	app := fiber.New(fiber.Config{
		DisableStartupMessage: true,
	})

	app.Get("/api/user", func(c *fiber.Ctx) error {
		return c.JSON(FiberBenchUserResponse{
			ID:       101,
			Name:     "Taylor Otwell",
			Email:    "taylor@laravel.com",
			Role:     "Administrator",
			IsActive: true,
		})
	})

	app.Get("/api/users/:id/posts/:post_id", func(c *fiber.Ctx) error {
		return c.JSON(FiberBenchPostResponse{
			UserID: c.Params("id"),
			PostID: c.Params("post_id"),
			Status: "published",
		})
	})

	log.Printf("Fiber benchmark server listening on %s", port)
	if err := app.Listen(port); err != nil {
		log.Fatal(err)
	}
}
