CREATE TABLE mfa_recovery_requests (
  id BINARY(16) PRIMARY KEY,
  user_id BINARY(16) NOT NULL,
  initiated_by BINARY(16) NOT NULL,
  reviewed_by BINARY(16),
  status ENUM('PENDING_REVIEW','APPROVED','REJECTED','EXPIRED') NOT NULL,
  identity_verification_note VARCHAR(1000) NOT NULL,
  review_note VARCHAR(1000),
  version BIGINT UNSIGNED NOT NULL DEFAULT 1,
  expires_at TIMESTAMP(6) NOT NULL,
  created_at TIMESTAMP(6) NOT NULL DEFAULT CURRENT_TIMESTAMP(6),
  reviewed_at TIMESTAMP(6),
  CONSTRAINT fk_recovery_user FOREIGN KEY(user_id) REFERENCES user_accounts(id),
  CONSTRAINT fk_recovery_initiator FOREIGN KEY(initiated_by) REFERENCES user_accounts(id),
  CONSTRAINT fk_recovery_reviewer FOREIGN KEY(reviewed_by) REFERENCES user_accounts(id),
  INDEX ix_recovery_status(status,expires_at),
  INDEX ix_recovery_user(user_id,created_at)
) ENGINE=InnoDB;
