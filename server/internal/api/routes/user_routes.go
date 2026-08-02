package routes

import (
	"net/http"
	"strconv"

	"github.com/gin-gonic/gin"
)

// RegisterUserRoutes mounts the user routes under the given group.
//
// Carried over from the Python scaffold: these return fake users and are
// not part of the product.
func RegisterUserRoutes(rg *gin.RouterGroup) {
	g := rg.Group("/users")
	g.GET("", getUsers)
	g.GET("/:user_id", getUser)
}

func getUsers(c *gin.Context) {
	c.JSON(http.StatusOK, []gin.H{
		{"username": "Alice"},
		{"username": "Bob"},
	})
}

func getUser(c *gin.Context) {
	id, err := strconv.Atoi(c.Param("user_id"))
	if err != nil {
		c.JSON(http.StatusUnprocessableEntity, gin.H{"detail": "user_id must be an integer"})
		return
	}
	c.JSON(http.StatusOK, gin.H{"user_id": id})
}
