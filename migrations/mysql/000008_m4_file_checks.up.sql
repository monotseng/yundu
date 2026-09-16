ALTER TABLE exchange_requests MODIFY status ENUM('DRAFT','CHECKING','SCANNING','SCAN_FAILED','IN_REVIEW','REJECTED_CHECK','PENDING_APPROVAL','APPROVED','COPYING','READY','PARTIALLY_DELIVERED','DELIVERED','REJECTED','CANCELLED','EXPIRED','REVOKED','QUARANTINED','BLOCKED') NOT NULL;
CREATE TABLE file_check_results (
  id BINARY(16) PRIMARY KEY, request_id BINARY(16) NOT NULL, file_id BINARY(16) NOT NULL,
  stage ENUM('SOURCE','PRETRANSFER','TARGET') NOT NULL, check_type ENUM('TEXT','TYPE','HASH','VIRUS') NOT NULL,
  required BOOLEAN NOT NULL, status ENUM('PASSED','FAILED','SKIPPED_DISABLED','ERROR','TIMEOUT','INFECTED','UNSUPPORTED','LIMIT_EXCEEDED') NOT NULL,
  rule_version VARCHAR(64) NOT NULL, engine VARCHAR(64) NOT NULL, engine_version VARCHAR(128), definitions_version VARCHAR(128),
  sha256 BINARY(32), checked_bytes BIGINT UNSIGNED NOT NULL DEFAULT 0, coverage_complete BOOLEAN NOT NULL DEFAULT FALSE,
  report_summary JSON NOT NULL, started_at TIMESTAMP(6) NOT NULL, finished_at TIMESTAMP(6) NOT NULL,
  CONSTRAINT fk_check_request FOREIGN KEY(request_id) REFERENCES exchange_requests(id), CONSTRAINT fk_check_file FOREIGN KEY(file_id) REFERENCES request_files(id),
  UNIQUE KEY uk_file_stage_type(file_id,stage,check_type), INDEX ix_check_request(request_id,stage,status)
) ENGINE=InnoDB;
