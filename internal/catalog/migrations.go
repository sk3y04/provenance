package catalog

const migration001 = `
CREATE TABLE IF NOT EXISTS archive_collections (
    id SERIAL PRIMARY KEY,
    name TEXT UNIQUE NOT NULL,
    vault_root TEXT NOT NULL,
    description TEXT DEFAULT '',
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE TABLE IF NOT EXISTS revisions (
    id TEXT PRIMARY KEY,
    collection_name TEXT NOT NULL REFERENCES archive_collections(name),
    source_url TEXT NOT NULL,
    source_kind TEXT NOT NULL,
    source_ref TEXT NOT NULL,
    tool TEXT NOT NULL,
    tool_version TEXT NOT NULL DEFAULT '',
    captured_at TIMESTAMPTZ NOT NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE TABLE IF NOT EXISTS entities (
    id SERIAL PRIMARY KEY,
    revision_id TEXT NOT NULL REFERENCES revisions(id),
    external_id TEXT NOT NULL,
    url TEXT NOT NULL,
    canonical_url TEXT DEFAULT '',
    title TEXT DEFAULT '',
    author TEXT DEFAULT '',
    published_at TIMESTAMPTZ,
    captured_at TIMESTAMPTZ NOT NULL,
    kind TEXT DEFAULT '',
    extractor TEXT DEFAULT '',
    raw_metadata JSONB DEFAULT '{}',
    search_vector TSVECTOR,
    UNIQUE(revision_id, external_id)
);

CREATE TABLE IF NOT EXISTS documents (
    id SERIAL PRIMARY KEY,
    revision_id TEXT NOT NULL REFERENCES revisions(id),
    entity_external_id TEXT NOT NULL,
    content TEXT NOT NULL,
    format TEXT NOT NULL DEFAULT 'plain',
    search_vector TSVECTOR
);

CREATE TABLE IF NOT EXISTS artifacts (
    sha256 TEXT PRIMARY KEY,
    revision_id TEXT NOT NULL REFERENCES revisions(id),
    path TEXT NOT NULL,
    size BIGINT NOT NULL DEFAULT 0,
    mime_type TEXT DEFAULT '',
    kind TEXT NOT NULL
);

CREATE TABLE IF NOT EXISTS relations (
    id SERIAL PRIMARY KEY,
    revision_id TEXT NOT NULL REFERENCES revisions(id),
    from_id TEXT NOT NULL,
    to_id TEXT NOT NULL,
    kind TEXT NOT NULL
);

DO $$
BEGIN
    IF NOT EXISTS (
        SELECT 1 FROM pg_indexes WHERE indexname = 'idx_entities_search'
    ) THEN
        CREATE INDEX idx_entities_search ON entities USING GIN (search_vector);
    END IF;
    IF NOT EXISTS (
        SELECT 1 FROM pg_indexes WHERE indexname = 'idx_documents_search'
    ) THEN
        CREATE INDEX idx_documents_search ON documents USING GIN (search_vector);
    END IF;
END $$;

CREATE OR REPLACE FUNCTION entities_search_update() RETURNS TRIGGER AS $func$
BEGIN
    NEW.search_vector :=
        setweight(to_tsvector('english', COALESCE(NEW.title, '')), 'A') ||
        setweight(to_tsvector('english', COALESCE(NEW.author, '')), 'B') ||
        setweight(to_tsvector('english', COALESCE(NEW.external_id, '')), 'C');
    RETURN NEW;
END;
$func$ LANGUAGE plpgsql;

DROP TRIGGER IF EXISTS entities_search_trigger ON entities;
CREATE TRIGGER entities_search_trigger
    BEFORE INSERT OR UPDATE ON entities
    FOR EACH ROW EXECUTE FUNCTION entities_search_update();

CREATE OR REPLACE FUNCTION documents_search_update() RETURNS TRIGGER AS $func$
BEGIN
    NEW.search_vector := to_tsvector('english', COALESCE(NEW.content, ''));
    RETURN NEW;
END;
$func$ LANGUAGE plpgsql;

DROP TRIGGER IF EXISTS documents_search_trigger ON documents;
CREATE TRIGGER documents_search_trigger
    BEFORE INSERT OR UPDATE ON documents
    FOR EACH ROW EXECUTE FUNCTION documents_search_update();
`
