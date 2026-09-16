CREATE TABLE upload_sessions (
  id BINARY(16) PRIMARY KEY, request_id BINARY(16) NOT NULL, file_id BINARY(16) NOT NULL UNIQUE, user_id BINARY(16) NOT NULL,
  expected_bytes BIGINT UNSIGNED NOT NULL, status ENUM('RESERVED','UPLOADING','COMPLETED','ABORTED','FAILED','EXPIRED') NOT NULL DEFAULT 'RESERVED',
  expires_at TIMESTAMP(6) NOT NULL, version BIGINT UNSIGNED NOT NULL DEFAULT 1, created_at TIMESTAMP(6) NOT NULL DEFAULT CURRENT_TIMESTAMP(6), updated_at TIMESTAMP(6) NOT NULL DEFAULT CURRENT_TIMESTAMP(6) ON UPDATE CURRENT_TIMESTAMP(6),
  CONSTRAINT fk_upload_request FOREIGN KEY(request_id) REFERENCES exchange_requests(id), CONSTRAINT fk_upload_file FOREIGN KEY(file_id) REFERENCES request_files(id), CONSTRAINT fk_upload_user FOREIGN KEY(user_id) REFERENCES user_accounts(id),
  INDEX ix_upload_owner(user_id,status,expires_at)
) ENGINE=InnoDB;
CREATE TABLE quota_counters (
  user_id BINARY(16) NOT NULL, direction ENUM('PROD_TO_OFFICE','OFFICE_TO_PROD') NOT NULL, period_start DATE NOT NULL,
  reserved_bytes BIGINT UNSIGNED NOT NULL DEFAULT 0, consumed_bytes BIGINT UNSIGNED NOT NULL DEFAULT 0, version BIGINT UNSIGNED NOT NULL DEFAULT 1,
  updated_at TIMESTAMP(6) NOT NULL DEFAULT CURRENT_TIMESTAMP(6) ON UPDATE CURRENT_TIMESTAMP(6), PRIMARY KEY(user_id,direction,period_start)
) ENGINE=InnoDB;
CREATE TABLE idempotency_records (
  actor_id BINARY(16) NOT NULL, route VARCHAR(191) NOT NULL, idempotency_key VARCHAR(128) NOT NULL, payload_sha256 BINARY(32) NOT NULL,
  status_code SMALLINT UNSIGNED NOT NULL, response_json JSON NOT NULL, expires_at TIMESTAMP(6) NOT NULL, created_at TIMESTAMP(6) NOT NULL DEFAULT CURRENT_TIMESTAMP(6),
  PRIMARY KEY(actor_id,route,idempotency_key), INDEX ix_idempotency_expiry(expires_at)
) ENGINE=InnoDB;
