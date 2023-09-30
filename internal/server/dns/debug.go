package dns

// Debug returns a dump of the current configuration.
func (s *Server) Debug(zone string) string {
	// Locking.
	s.mu.Lock()
	defer s.mu.Unlock()

	return s.debug(zone)
}

func (s *Server) debug(zone string) string {
	return ""
}
