package main

import (
	"log"
	"os"

	"github.com/gin-gonic/gin"
)

type GinBenchUserResponse struct {
	ID       int    `json:"id"`
	Name     string `json:"name"`
	Email    string `json:"email"`
	Role     string `json:"role"`
	IsActive bool   `json:"is_active"`
}

type GinBenchPostResponse struct {
	UserID string `json:"user_id"`
	PostID string `json:"post_id"`
	Status string `json:"status"`
}

func main() {
	gin.SetMode(gin.ReleaseMode)
	port := os.Getenv("PORT")
	if port == "" {
		port = ":9002"
	}

	app := gin.New()

	app.GET("/api/user", func(c *gin.Context) {
		c.JSON(200, GinBenchUserResponse{
			ID:       101,
			Name:     "Taylor Otwell",
			Email:    "taylor@laravel.com",
			Role:     "Administrator",
			IsActive: true,
		})
	})

	app.GET("/api/users/:id/posts/:post_id", func(c *gin.Context) {
		c.JSON(200, GinBenchPostResponse{
			UserID: c.Param("id"),
			PostID: c.Param("post_id"),
			Status: "published",
		})
	})

	log.Printf("Gin benchmark server listening on %s", port)
	if err := app.Run(port); err != nil {
		log.Fatal(err)
	}
}
