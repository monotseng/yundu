CREATE TABLE business_systems (
  id BINARY(16) PRIMARY KEY, code VARCHAR(64) NOT NULL UNIQUE, name VARCHAR(128) NOT NULL,
  department_id BINARY(16), status ENUM('ACTIVE','DISABLED') NOT NULL DEFAULT 'ACTIVE',
  allowed_directions JSON NOT NULL, version BIGINT UNSIGNED NOT NULL DEFAULT 1,
  created_at TIMESTAMP(6) NOT NULL DEFAULT CURRENT_TIMESTAMP(6), updated_at TIMESTAMP(6) NOT NULL DEFAULT CURRENT_TIMESTAMP(6) ON UPDATE CURRENT_TIMESTAMP(6),
  CONSTRAINT fk_business_system_department FOREIGN KEY(department_id) REFERENCES departments(id)
) ENGINE=InnoDB;

CREATE TABLE secret_records (
  id BINARY(16) PRIMARY KEY, name VARCHAR(128) NOT NULL, purpose VARCHAR(64) NOT NULL,
  ciphertext VARBINARY(4096) NOT NULL, key_revision VARCHAR(64) NOT NULL DEFAULT 'master-v1',
  status ENUM('ACTIVE','RETIRED') NOT NULL DEFAULT 'ACTIVE', created_by BINARY(16) NOT NULL,
  created_at TIMESTAMP(6) NOT NULL DEFAULT CURRENT_TIMESTAMP(6), retired_at TIMESTAMP(6),
  CONSTRAINT fk_secret_creator FOREIGN KEY(created_by) REFERENCES user_accounts(id), INDEX ix_secret_purpose(purpose,status)
) ENGINE=InnoDB;

CREATE TABLE integration_definitions (
  id BINARY(16) PRIMARY KEY, name VARCHAR(128) NOT NULL, type ENUM('S3_STORAGE','HTTP_PROXY','WECOM_GROUP_BOT','SMTP','LLM','CLAMAV','ZABBIX_HTTP') NOT NULL,
  zone ENUM('OFFICE','PRODUCTION','SHARED','ADMIN') NOT NULL, status ENUM('DRAFT','PUBLISHED','DISABLED') NOT NULL DEFAULT 'DRAFT',
  current_version_id BINARY(16), version BIGINT UNSIGNED NOT NULL DEFAULT 1, created_by BINARY(16) NOT NULL,
  created_at TIMESTAMP(6) NOT NULL DEFAULT CURRENT_TIMESTAMP(6), updated_at TIMESTAMP(6) NOT NULL DEFAULT CURRENT_TIMESTAMP(6) ON UPDATE CURRENT_TIMESTAMP(6),
  CONSTRAINT fk_integration_creator FOREIGN KEY(created_by) REFERENCES user_accounts(id), INDEX ix_integration_type(type,status)
) ENGINE=InnoDB;
CREATE TABLE integration_versions (
  id BINARY(16) PRIMARY KEY, integration_id BINARY(16) NOT NULL, revision INT UNSIGNED NOT NULL,
  config_json JSON NOT NULL, config_sha256 BINARY(32) NOT NULL, status ENUM('DRAFT','PUBLISHED','RETIRED') NOT NULL,
  created_by BINARY(16) NOT NULL, created_at TIMESTAMP(6) NOT NULL DEFAULT CURRENT_TIMESTAMP(6), published_by BINARY(16), published_at TIMESTAMP(6),
  UNIQUE KEY uk_integration_revision(integration_id,revision), CONSTRAINT fk_integration_version_definition FOREIGN KEY(integration_id) REFERENCES integration_definitions(id),
  CONSTRAINT fk_integration_version_creator FOREIGN KEY(created_by) REFERENCES user_accounts(id)
) ENGINE=InnoDB;
ALTER TABLE integration_definitions ADD CONSTRAINT fk_integration_current_version FOREIGN KEY(current_version_id) REFERENCES integration_versions(id);
CREATE TABLE integration_version_secrets (
  integration_version_id BINARY(16) NOT NULL, field_name VARCHAR(64) NOT NULL, secret_id BINARY(16) NOT NULL,
  PRIMARY KEY(integration_version_id,field_name), CONSTRAINT fk_ivs_version FOREIGN KEY(integration_version_id) REFERENCES integration_versions(id),
  CONSTRAINT fk_ivs_secret FOREIGN KEY(secret_id) REFERENCES secret_records(id)
) ENGINE=InnoDB;
CREATE TABLE integration_test_runs (
  id BINARY(16) PRIMARY KEY, integration_version_id BINARY(16) NOT NULL, status ENUM('RUNNING','PASSED','FAILED') NOT NULL,
  stage VARCHAR(64), error_code VARCHAR(64), safe_detail VARCHAR(1000), started_by BINARY(16) NOT NULL,
  started_at TIMESTAMP(6) NOT NULL DEFAULT CURRENT_TIMESTAMP(6), finished_at TIMESTAMP(6),
  CONSTRAINT fk_test_version FOREIGN KEY(integration_version_id) REFERENCES integration_versions(id), INDEX ix_test_version(integration_version_id,started_at)
) ENGINE=InnoDB;
CREATE TABLE exchange_channels (
  id BINARY(16) PRIMARY KEY, direction ENUM('PROD_TO_OFFICE','OFFICE_TO_PROD') NOT NULL UNIQUE,
  source_storage_version_id BINARY(16) NOT NULL, target_storage_version_id BINARY(16) NOT NULL,
  antivirus_enabled BOOLEAN NOT NULL DEFAULT FALSE, status ENUM('DRAFT','PUBLISHED','DISABLED') NOT NULL DEFAULT 'DRAFT',
  version BIGINT UNSIGNED NOT NULL DEFAULT 1, updated_by BINARY(16) NOT NULL, updated_at TIMESTAMP(6) NOT NULL DEFAULT CURRENT_TIMESTAMP(6) ON UPDATE CURRENT_TIMESTAMP(6),
  CONSTRAINT fk_channel_source FOREIGN KEY(source_storage_version_id) REFERENCES integration_versions(id),
  CONSTRAINT fk_channel_target FOREIGN KEY(target_storage_version_id) REFERENCES integration_versions(id)
) ENGINE=InnoDB;
