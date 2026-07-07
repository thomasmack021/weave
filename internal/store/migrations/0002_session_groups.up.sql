-- A session is a point-in-time snapshot of the principal. The subject is
-- reachable via user_id, but the IdP groups are asserted per login and are not
-- stored on the user, so snapshot them here to make a session a self-contained
-- credential (a cookie-authenticated request needs no proxy headers).
ALTER TABLE sessions ADD COLUMN groups TEXT[] NOT NULL DEFAULT '{}';
