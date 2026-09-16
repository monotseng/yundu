package monitoring

import (
	"fmt"
	"strings"
)

const ZabbixTemplate = `{
  "zabbix_export": {
    "version": "7.0",
    "template_groups": [{"uuid":"8e17fc779ad4463ba6675344d4437f9d","name":"Templates/Applications"}],
    "templates": [{
      "uuid":"28f752cde15644cab3e628e2d941f969",
      "template":"Yundu HTTP Monitoring",
      "name":"云渡 HTTP 监控",
      "groups":[{"name":"Templates/Applications"}],
      "macros":[
        {"macro":"{$YUNDU.URL}","value":"https://yundu-admin.example.internal"},
        {"macro":"{$YUNDU.TOKEN}","type":"SECRET_TEXT"}
      ],
      "items":[
        {"uuid":"f3ec024934f143d28d7b9864d62c563f","name":"云渡监控快照","type":"HTTP_AGENT","key":"yundu.snapshot","delay":"30s","history":"0","value_type":"TEXT","url":"{$YUNDU.URL}/api/v1/monitoring/zabbix","headers":[{"name":"Authorization","value":"Bearer {$YUNDU.TOKEN}"}],"follow_redirects":"NO","verify_peer":"YES","verify_host":"YES","timeout":"10s","status_codes":"200"},
        {"uuid":"304e8f475fba4870b3dff03c3d946d9a","name":"快照有效","type":"DEPENDENT","key":"yundu.snapshot.valid","delay":"0","history":"7d","preprocessing":[{"type":"JSONPATH","parameters":["$.snapshot_valid"]}],"master_item":{"key":"yundu.snapshot"}},
        {"uuid":"66ed4b264056468b8f7f71b1615529ea","name":"快照时间","type":"DEPENDENT","key":"yundu.snapshot.updated_at","delay":"0","history":"7d","value_type":"TEXT","preprocessing":[{"type":"JSONPATH","parameters":["$.snapshot_updated_at"]}],"master_item":{"key":"yundu.snapshot"}},
        DEPENDENT_ITEMS
      ]
    }]
  }
}`

var ZabbixMetricKeys = []string{"open_policy_breach_incidents", "open_integrity_incidents", "cross_zone_denied_5m", "unauthorized_download_denied_5m", "text_denied_5m", "size_denied_5m", "quota_denied_5m", "resource_busy_5m", "max_user_upload_attempts_5m", "max_user_download_starts_5m", "max_user_upload_bytes_1h", "max_user_download_bytes_1h", "active_uploads", "active_downloads", "active_copies", "audit_aggregation_lag_seconds"}

func RenderZabbixTemplate() string {
	parts := make([]string, 0, len(ZabbixMetricKeys))
	for i, key := range ZabbixMetricKeys {
		parts = append(parts, fmt.Sprintf(`{"uuid":"%032x","name":"%s","type":"DEPENDENT","key":"yundu.%s","delay":"0","history":"7d","preprocessing":[{"type":"JSONPATH","parameters":["$.%s"]}],"master_item":{"key":"yundu.snapshot"}}`, i+100, key, key, key))
	}
	return strings.Replace(ZabbixTemplate, "DEPENDENT_ITEMS", strings.Join(parts, ","), 1)
}
