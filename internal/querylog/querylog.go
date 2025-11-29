package querylog

import (
	"bufio"
	"encoding/json"
	"log"
	"os"
	"strings"
	"sync"
	"time"
)

type LogEntry struct {
	ID         int64     `json:"id"`
	Time       time.Time `json:"time"`
	ClientIP   string    `json:"client_ip"`
	Domain     string    `json:"domain"`
	Type       string    `json:"type"`
	Upstream   string    `json:"upstream"`
	Answer     string    `json:"answer"`
	DurationMs int64     `json:"duration_ms"`
	Status     string    `json:"status"`
}

type Stats struct {
	TotalQueries  int64            `json:"total_queries"`
	TotalCN       int64            `json:"total_cn"`
	TotalOverseas int64            `json:"total_overseas"`
	StartTime     time.Time        `json:"start_time"`
	TopClients    map[string]int64 `json:"top_clients"`
	TopDomains    map[string]int64 `json:"top_domains"`
}

type QueryLogger struct {
	mu         sync.RWMutex
	logs       []*LogEntry
	maxSizeMB  int
	nextID     int64
	filePath   string
	saveToFile bool
	stats      Stats
}

const maxMemoryLogs = 5000

func NewQueryLogger(maxSizeMB int, filePath string, saveToFile bool) *QueryLogger {
	if maxSizeMB <= 0 {
		maxSizeMB = 1
	}
	l := &QueryLogger{
		logs:       make([]*LogEntry, 0, maxMemoryLogs),
		maxSizeMB:  maxSizeMB,
		nextID:     1,
		filePath:   filePath,
		saveToFile: saveToFile,
		stats: Stats{
			StartTime:  time.Now(),
			TopClients: make(map[string]int64),
			TopDomains: make(map[string]int64),
		},
	}

	if saveToFile && filePath != "" {
		l.loadFromFile()
	}

	return l
}

func (l *QueryLogger) loadFromFile() {
	f, err := os.Open(l.filePath)
	if err != nil {
		if !os.IsNotExist(err) {
			log.Printf("Error opening log file: %v", err)
		}
		return
	}
	defer f.Close()

	scanner := bufio.NewScanner(f)
	for scanner.Scan() {
		var entry LogEntry
		if err := json.Unmarshal(scanner.Bytes(), &entry); err == nil {
			l.updateStats(&entry)
			l.addToMemory(&entry)
			if entry.ID >= l.nextID {
				l.nextID = entry.ID + 1
			}
		}
	}
}

func (l *QueryLogger) AddLog(entry *LogEntry) {
	l.mu.Lock()
	defer l.mu.Unlock()

	entry.ID = l.nextID
	l.nextID++
	if entry.Time.IsZero() {
		entry.Time = time.Now()
	}

	l.updateStats(entry)
	l.addToMemory(entry)

	if l.saveToFile && l.filePath != "" {
		go l.appendToFile(*entry)
	}
}

func (l *QueryLogger) updateStats(entry *LogEntry) {
	l.stats.TotalQueries++
	if strings.Contains(entry.Upstream, "CN") {
		l.stats.TotalCN++
	} else if strings.Contains(entry.Upstream, "Overseas") {
		l.stats.TotalOverseas++
	}
	l.stats.TopClients[entry.ClientIP]++
	l.stats.TopDomains[entry.Domain]++
}

func (l *QueryLogger) addToMemory(entry *LogEntry) {
	l.logs = append(l.logs, entry)
	if len(l.logs) > maxMemoryLogs {
		l.logs = l.logs[1:]
	}
}

func (l *QueryLogger) appendToFile(entry LogEntry) {
	fi, err := os.Stat(l.filePath)
	if err == nil && fi.Size() >= int64(l.maxSizeMB)*1024*1024 {
		os.Rename(l.filePath, l.filePath+".old")
	}

	data, err := json.Marshal(entry)
	if err != nil {
		return
	}

	f, err := os.OpenFile(l.filePath, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0644)
	if err != nil {
		log.Printf("Error writing to log file: %v", err)
		return
	}
	defer f.Close()

	if _, err := f.Write(data); err != nil {
		log.Printf("Error writing data to log file: %v", err)
		return
	}
	f.WriteString("\n")
}

func (l *QueryLogger) GetLogs(offset, limit int, search string) ([]*LogEntry, int64) {
	l.mu.RLock()
	defer l.mu.RUnlock()

	var result []*LogEntry
	var count int64 = 0
	searchLower := strings.ToLower(search)

	for i := len(l.logs) - 1; i >= 0; i-- {
		entry := l.logs[i]

		if searchLower != "" {
			match := strings.Contains(strings.ToLower(entry.ClientIP), searchLower) ||
				strings.Contains(strings.ToLower(entry.Domain), searchLower) ||
				strings.Contains(strings.ToLower(entry.Type), searchLower) ||
				strings.Contains(strings.ToLower(entry.Upstream), searchLower) ||
				strings.Contains(strings.ToLower(entry.Answer), searchLower) ||
				strings.Contains(strings.ToLower(entry.Status), searchLower)
			if !match {
				continue
			}
		}

		if count >= int64(offset) && len(result) < limit {
			result = append(result, entry)
		}
		count++
	}

	return result, count
}

func (l *QueryLogger) GetStats() Stats {
	l.mu.RLock()
	defer l.mu.RUnlock()

	s := l.stats
	s.TopClients = make(map[string]int64, len(l.stats.TopClients))
	for k, v := range l.stats.TopClients {
		s.TopClients[k] = v
	}
	s.TopDomains = make(map[string]int64, len(l.stats.TopDomains))
	for k, v := range l.stats.TopDomains {
		s.TopDomains[k] = v
	}

	return s
}

func (l *QueryLogger) Clear() {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.logs = make([]*LogEntry, 0, maxMemoryLogs)
}
