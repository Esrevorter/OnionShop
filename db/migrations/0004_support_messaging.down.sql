-- Reverse of 0004_support_messaging.sql
BEGIN;

DROP TABLE IF EXISTS messages;
DROP TABLE IF EXISTS threads;
DROP TABLE IF EXISTS ticket_messages;
DROP TABLE IF EXISTS tickets;

COMMIT;
