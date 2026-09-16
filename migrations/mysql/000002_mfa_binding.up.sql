ALTER TABLE activation_tokens MODIFY purpose ENUM('ACTIVATE','MFA_BIND','MFA_REBIND') NOT NULL;

CREATE TABLE mfa_binding_tokens (
  id BINARY(16) PRIMARY KEY,
  user_id BINARY(16) NOT NULL,
  token_hash BINARY(32) NOT NULL UNIQUE,
  purpose ENUM('INITIAL_BIND','REBIND') NOT NULL,
  expires_at TIMESTAMP(6) NOT NULL,
  consumed_at TIMESTAMP(6),
  attempts SMALLINT UNSIGNED NOT NULL DEFAULT 0,
  created_at TIMESTAMP(6) NOT NULL DEFAULT CURRENT_TIMESTAMP(6),
  CONSTRAINT fk_mfa_binding_user FOREIGN KEY(user_id) REFERENCES user_accounts(id),
  INDEX ix_mfa_binding_expiry(expires_at)
) ENGINE=InnoDB;
