-- CREATE DATABASE Netspace;

CREATE TABLE Locations (
	id SERIAL PRIMARY KEY,
	slug VARCHAR(55) UNIQUE NOT NULL,
	name VARCHAR(100) UNIQUE NOT NULL,
	address TEXT UNIQUE NOT NULL,
	partnerId TEXT NOT NULL,
	joinDate TIMESTAMPTZ DEFAULT NOW(),
	capacity INT CHECK (capacity >= 1),
	timezone TEXT DEFAULT 'Asia/Jakarta',
	isActive BOOL DEFAULT TRUE,
	qrToken TEXT NOT NULL,
	qrLabel TEXT NOT NULL,
	latitude DOUBLE PRECISION,
	longitude DOUBLE PRECISION,
	geofenceRadius INT DEFAULT 100
);

-- Migration for existing databases: add geofence columns (venue center + radius
-- in meters) used for GPS auto-logout. Safe to run repeatedly.
ALTER TABLE Locations ADD COLUMN IF NOT EXISTS latitude DOUBLE PRECISION;
ALTER TABLE Locations ADD COLUMN IF NOT EXISTS longitude DOUBLE PRECISION;
ALTER TABLE Locations ADD COLUMN IF NOT EXISTS geofenceRadius INT DEFAULT 100;

CREATE TABLE Users (
	id TEXT UNIQUE NOT NULL,
	locationId INT REFERENCES Locations(id),
	name TEXT,
	slug TEXT,
	age INT,
	gender TEXT,
	occupation TEXT,
	createdAt TIMESTAMPTZ DEFAULT NOW(),
	isActive BOOL DEFAULT TRUE
);

-- Migration for existing databases: add the occupation ("Pekerjaan saat ini")
-- column captured at check-in. Safe to run repeatedly.
ALTER TABLE Users ADD COLUMN IF NOT EXISTS occupation TEXT;

CREATE TABLE Interests (
	id SERIAL PRIMARY KEY,
	emoji TEXT NOT NULL,
	label TEXT NOT NULL
);

CREATE TABLE UserInterests (
	userId TEXT REFERENCES Users(id),
	interestId INT REFERENCES Interests(id),
	PRIMARY KEY(userId, interestId)
);

CREATE TABLE UserCustomInterests (
	userId TEXT REFERENCES Users(id),
	emoji TEXT NOT NULL,
	label TEXT NOT NULL,
	PRIMARY KEY(userId, label)
);

CREATE TABLE PrivateMessages (
	messageid TEXT PRIMARY KEY NOT NULL,
	locationId INT REFERENCES Locations(id),
	senderId TEXT REFERENCES Users(id),
	recipientId TEXT REFERENCES Users(id),
	"message" TEXT NOT NULL,
	"timestamp" TIMESTAMPTZ DEFAULT NOW(),
	isread BOOL DEFAULT FALSE
);

-- Migration for existing databases: read receipts (WhatsApp-style). A message
-- flips to isread=true once the recipient opens the chat with the sender; the
-- sender's bubble then shows blue double-checks. Safe to run repeatedly.
ALTER TABLE PrivateMessages ADD COLUMN IF NOT EXISTS isread BOOL DEFAULT FALSE;

CREATE TABLE PublicMessages (
	messageId  TEXT PRIMARY KEY NOT NULL,
	locationId INT REFERENCES Locations(id),
	senderId   TEXT REFERENCES Users(id),
	"message" TEXT NOT NULL,
	"timestamp" TIMESTAMPTZ DEFAULT NOW()
);

CREATE TABLE Groups (
	Id TEXT PRIMARY KEY NOT NULL,
	Name TEXT,
	IsActive BOOL DEFAULT TRUE	
);

CREATE TABLE GroupMembers (
	groupid TEXT REFERENCES "groups"(id),
	userid TEXT REFERENCES Users(id),
	PRIMARY KEY(groupid, userid)
);

CREATE TABLE GroupMessages (
	messageId  TEXT PRIMARY KEY NOT NULL,
	locationId INT REFERENCES Locations(id),
	senderId   TEXT REFERENCES Users(id),
	groupId TEXT REFERENCES Groups(id),
	"message" TEXT NOT NULL,
	"timestamp" TIMESTAMPTZ DEFAULT NOW()
);

CREATE TABLE Notifications (
	"id" TEXT PRIMARY KEY,
	userId TEXT REFERENCES Users(id),
	"type" TEXT CHECK ("type" IN ('message', 'group_invite', 'chat_request', 'system')),
	emoji TEXT NOT NULL,
	avatarGradient TEXT NOT NULL,
	title TEXT NOT NULL,
	description TEXT NOT NULL,    
	"timestamp" TIMESTAMPTZ DEFAULT NOW(),
	unread BOOL DEFAULT TRUE,
	primaryLabel TEXT,
	secondaryLabel TEXT,
	groupid TEXT
);

-- Migration for existing databases: group_invite notifications need to remember
-- which group they belong to so the recipient can still accept after a re-fetch
-- (the live WS event already carried it; the DB row previously dropped it).
-- Safe to run repeatedly.
ALTER TABLE Notifications ADD COLUMN IF NOT EXISTS groupid TEXT;

-- Migration for existing databases: message notifications remember who sent them
-- (the actor's user id) so tapping the notification can open the DM with that
-- person. Empty for non-message notifs. Safe to run repeatedly.
ALTER TABLE Notifications ADD COLUMN IF NOT EXISTS senderid TEXT;

CREATE TABLE Admins (
	"id" TEXT PRIMARY KEY,
	username TEXT,
	"password" TEXT,
	"role" TEXT,
	"plan" TEXT,
	"avatar" TEXT,
	"name" TEXT,
	locationId INT REFERENCES Locations(id)
);

ALTER TABLE Admins ADD COLUMN IF NOT EXISTS locationId INT REFERENCES Locations(id);
ALTER TABLE PublicMessages ADD COLUMN IF NOT EXISTS adminId TEXT REFERENCES Admins(id);
CREATE INDEX IF NOT EXISTS idx_publicmessages_location_timestamp
	ON PublicMessages(locationId, "timestamp" DESC);

INSERT INTO Interests (emoji, label)
VALUES ('☕', 'Kopi'),
  ('🎮', 'Gaming'),
  ('📚', 'Buku'),
  ('🎵', 'Musik'),
  ('🍜', 'Kuliner'),
  ('✈️', 'Travel'),
  ('💻', 'Tech'),
  ('🎨', 'Seni'),
  ('🏋️', 'Olahraga'),
  ('🎬', 'Film'),
  ('📷', 'Fotografi'),
  ('🌱', 'Tanaman');

-- ── Dev seed: Locations (slug harus cocok dengan URL /[location]/admin di FE) ──
-- latitude/longitude = pusat geofence; geofenceRadius dalam meter.
-- kopiloka memakai koordinat asli; koktong & kopi-braga perkiraan dari alamat
-- (sesuaikan dengan titik venue sebenarnya bila perlu).
INSERT INTO Locations (slug, name, address, partnerId, capacity, qrToken, qrLabel, latitude, longitude, geofenceRadius)
VALUES
  ('kopiloka', 'Kopiloka Sudirman', 'Jl. Jend. Sudirman No. 123, Jakarta Selatan', 'KPL-001', 40, 'kpl-001-m1-a8f4', 'Meja 1 · Kopiloka Sudirman', -6.201249, 106.782261, 100),
  ('koktong', 'Koktong', 'Jl. Pangeran Jayakarta No. 73, Jakarta Barat', 'KKT-001', 30, 'kkt-001-m1-b2c3', 'Meja 1 · Koktong', -6.138300, 106.821000, 100),
  ('kopi-braga', 'Kopi Braga', 'Jl. Braga No. 45, Bandung', 'KBG-001', 25, 'kbg-001-m1-d4e5', 'Meja 1 · Kopi Braga', -6.916800, 107.609700, 100);

-- Backfill coordinates for databases seeded before the geofence columns existed.
-- Idempotent: re-running just re-sets the same values.
UPDATE Locations SET latitude = -6.201249, longitude = 106.782261, geofenceRadius = 100 WHERE slug = 'kopiloka';
UPDATE Locations SET latitude = -6.138300, longitude = 106.821000, geofenceRadius = 100 WHERE slug = 'koktong';
UPDATE Locations SET latitude = -6.916800, longitude = 107.609700, geofenceRadius = 100 WHERE slug = 'kopi-braga';

-- ── Dev seed: Admins ──
-- Passwords are bcrypt hashes of the public demo password "admin123".
-- Change them before any real deployment. (Rows that still hold plaintext
-- from older seeds are upgraded to bcrypt automatically on first login.)
INSERT INTO Admins (id, username, password, role, plan, avatar, name)
VALUES
  ('adm-kpl', 'kopiloka', '$2a$10$X4lX4ghero6eVKZyotJASufwtnkBAqTrYlmjuOgtaO3vxh9w4VSjy', 'Partner', 'Pro Plan', '☕', 'Kopiloka Sudirman'),
  ('adm-kkt', 'koktong', '$2a$10$X4lX4ghero6eVKZyotJASufwtnkBAqTrYlmjuOgtaO3vxh9w4VSjy', 'Partner', 'Pro Plan', '🍵', 'Koktong'),
  ('adm-kbg', 'kopibraga', '$2a$10$X4lX4ghero6eVKZyotJASufwtnkBAqTrYlmjuOgtaO3vxh9w4VSjy', 'Partner', 'Pro Plan', '☕', 'Kopi Braga');

UPDATE Admins SET locationId = (SELECT id FROM Locations WHERE slug = 'kopiloka')
	WHERE id = 'adm-kpl';
UPDATE Admins SET locationId = (SELECT id FROM Locations WHERE slug = 'koktong')
	WHERE id = 'adm-kkt';
UPDATE Admins SET locationId = (SELECT id FROM Locations WHERE slug = 'kopi-braga')
	WHERE id = 'adm-kbg';
UPDATE Admins AS a SET locationId = l.id
	FROM Locations AS l
	WHERE a.locationId IS NULL
		AND replace(lower(a.username), '-', '') = replace(lower(l.slug), '-', '');
ALTER TABLE Admins ALTER COLUMN locationId SET NOT NULL;

ALTER TABLE PublicMessages DROP CONSTRAINT IF EXISTS publicmessages_sender_check;
ALTER TABLE PublicMessages ADD CONSTRAINT publicmessages_sender_check
	CHECK ((senderId IS NOT NULL)::int + (adminId IS NOT NULL)::int = 1);

-- Query bantu saat dev (uncomment manual kalau perlu):
-- SELECT * FROM Locations;
-- SELECT * FROM Admins;
-- SELECT * FROM interests;
