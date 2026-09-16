INSERT IGNORE INTO role_permissions(role_id,permission) SELECT id,'request.revoke' FROM roles WHERE code='SECURITY_ADMIN';
