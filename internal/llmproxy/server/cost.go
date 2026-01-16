package server

import (
	"context"
	"log"
	"sync"
	"time"

	"github.com/cloud-shuttle/drover/internal/llmproxy"
)

// CostTracker tracks API usage costs and enforces budget limits
type CostTracker struct {
	config      llmproxy.CostBudgetConfig
	hourlyCost  float64
	dailyCost   float64
	hourlyReset time.Time
	dailyReset  time.Time
	mu          sync.RWMutex
}

// NewCostTracker creates a new cost tracker
func NewCostTracker(config llmproxy.CostBudgetConfig) *CostTracker {
	now := time.Now()

	return &CostTracker{
		config:      config,
		hourlyReset: now.Truncate(time.Hour).Add(time.Hour),
		dailyReset:  now.Truncate(24 * time.Hour).Add(24 * time.Hour),
	}
}

// RecordCost records a cost and updates running totals
func (c *CostTracker) RecordCost(cost *llmproxy.Cost) {
	if !c.config.Enabled {
		return
	}

	c.mu.Lock()
	defer c.mu.Unlock()

	c.resetIfNeeded()

	c.hourlyCost += cost.TotalCost
	c.dailyCost += cost.TotalCost

	log.Printf("Cost: %s - %s: $%.4f (Hourly: $%.4f, Daily: $%.4f)",
		cost.Provider, cost.Model, cost.TotalCost, c.hourlyCost, c.dailyCost)
}

// CheckBudget checks if the budget allows for more requests
func (c *CostTracker) CheckBudget(ctx context.Context) bool {
	if !c.config.Enabled {
		return true
	}

	c.mu.RLock()
	defer c.mu.RUnlock()

	c.resetIfNeeded()

	// Check hourly limit
	if c.config.HourlyLimit > 0 && c.hourlyCost >= c.config.HourlyLimit {
		log.Printf("Hourly budget exceeded: $%.2f >= $%.2f", c.hourlyCost, c.config.HourlyLimit)
		return false
	}

	// Check daily limit
	if c.config.DailyLimit > 0 && c.dailyCost >= c.config.DailyLimit {
		log.Printf("Daily budget exceeded: $%.2f >= $%.2f", c.dailyCost, c.config.DailyLimit)
		return false
	}

	return true
}

// GetStats returns current cost statistics
func (c *CostTracker) GetStats() map[string]interface{} {
	c.mu.RLock()
	defer c.mu.RUnlock()

	c.resetIfNeeded()

	return map[string]interface{}{
		"cost_hourly":      c.hourlyCost,
		"cost_daily":       c.dailyCost,
		"budget_hourly":    c.config.HourlyLimit,
		"budget_daily":     c.config.DailyLimit,
		"budget_enabled":   c.config.Enabled,
		"hourly_remaining": max(0, c.config.HourlyLimit-c.hourlyCost),
		"daily_remaining":  max(0, c.config.DailyLimit-c.dailyCost),
		"hourly_reset_in":  time.Until(c.hourlyReset).String(),
		"daily_reset_in":   time.Until(c.dailyReset).String(),
	}
}

// resetIfNeeded resets hourly/daily counters if the time has passed
func (c *CostTracker) resetIfNeeded() {
	now := time.Now()

	if now.After(c.hourlyReset) {
		c.hourlyCost = 0
		c.hourlyReset = now.Truncate(time.Hour).Add(time.Hour)
	}

	if now.After(c.dailyReset) {
		c.dailyCost = 0
		c.dailyReset = now.Truncate(24 * time.Hour).Add(24 * time.Hour)
	}
}

func max(a, b float64) float64 {
	if a > b {
		return a
	}
	return b
}
