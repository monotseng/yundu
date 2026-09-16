CREATE TABLE auth_rate_limits (
  scope ENUM('PASSWORD_ACCOUNT','PASSWORD_IP','MFA_ACCOUNT','MFA_IP') NOT NULL,
  subject_hash BINARY(32) NOT NULL,
  window_started_at TIMESTAMP(6) NOT NULL,
  failure_count INT UNSIGNED NOT NULL DEFAULT 0,
  locked_until TIMESTAMP(6),
  updated_at TIMESTAMP(6) NOT NULL DEFAULT CURRENT_TIMESTAMP(6) ON UPDATE CURRENT_TIMESTAMP(6),
  PRIMARY KEY(scope,subject_hash),
  INDEX ix_auth_lock_expiry(locked_until)
) ENGINE=InnoDB;
