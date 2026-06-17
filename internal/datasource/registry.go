package datasource

func (r *Registry) List() []DataSource {
	items := make([]DataSource, 0, len(r.sources))
	for _, source := range r.sources {
		items = append(items, source)
	}
	return items
}
