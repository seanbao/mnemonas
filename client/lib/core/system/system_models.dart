final class HealthStatus {
  const HealthStatus({
    required this.status,
    required this.timestamp,
    required this.uptimeSeconds,
    required this.version,
    this.dataplaneHealthy,
  });

  factory HealthStatus.fromJson(Map<String, dynamic> json) {
    final status = _requiredString(json, 'status');
    if (status != 'healthy' && status != 'degraded') {
      throw const FormatException('Invalid health status');
    }
    final timestamp = DateTime.tryParse(_requiredString(json, 'timestamp'));
    final uptimeSeconds = json['uptime_secs'];
    if (timestamp == null || uptimeSeconds is! num || uptimeSeconds < 0) {
      throw const FormatException('Invalid health response');
    }
    final dataplane = _optionalMap(json['dataplane']);
    return HealthStatus(
      status: status,
      timestamp: timestamp.toUtc(),
      uptimeSeconds: uptimeSeconds.toInt(),
      version: _requiredString(json, 'version'),
      dataplaneHealthy: dataplane?['healthy'] as bool?,
    );
  }

  final String status;
  final DateTime timestamp;
  final int uptimeSeconds;
  final String version;
  final bool? dataplaneHealthy;

  bool get isHealthy => status == 'healthy';
}

final class ServerVersion {
  const ServerVersion({
    required this.name,
    required this.version,
    required this.buildTime,
    required this.goVersion,
  });

  factory ServerVersion.fromJson(Map<String, dynamic> json) {
    final name = _requiredString(json, 'name');
    if (name != 'MnemoNAS') {
      throw const FormatException('The endpoint is not a MnemoNAS server');
    }
    return ServerVersion(
      name: name,
      version: _requiredString(json, 'version'),
      buildTime: _requiredString(json, 'build_time'),
      goVersion: _requiredString(json, 'go'),
    );
  }

  final String name;
  final String version;
  final String buildTime;
  final String goVersion;
}

final class SetupStatus {
  const SetupStatus({
    required this.isFirstRun,
    required this.authEnabled,
    required this.shareEnabled,
    required this.webDavEnabled,
    required this.allowUnsafeNoAuth,
  });

  factory SetupStatus.fromJson(Map<String, dynamic> json) {
    if (json['success'] != true) {
      throw const FormatException('Invalid setup response');
    }
    return SetupStatus(
      isFirstRun: json['is_first_run'] == true,
      authEnabled: json['auth_enabled'] == true,
      shareEnabled: json['share_enabled'] == true,
      webDavEnabled: json['webdav_enabled'] == true,
      allowUnsafeNoAuth: json['allow_unsafe_no_auth'] == true,
    );
  }

  final bool isFirstRun;
  final bool authEnabled;
  final bool shareEnabled;
  final bool webDavEnabled;
  final bool allowUnsafeNoAuth;
}

final class ServerProbe {
  const ServerProbe({
    required this.health,
    required this.version,
    required this.setup,
  });

  final HealthStatus health;
  final ServerVersion version;
  final SetupStatus setup;
}

final class StorageStats {
  const StorageStats({
    required this.totalFilesAvailable,
    required this.storageStatsAvailable,
    required this.diskStatsAvailable,
    this.totalFiles,
    this.totalSize,
    this.uniqueSize,
    this.dedupRatio,
    this.diskTotal,
    this.diskUsed,
    this.diskAvailable,
    this.diskUsageRatio,
  });

  factory StorageStats.fromJson(Map<String, dynamic> json) {
    return StorageStats(
      totalFilesAvailable: json['total_files_available'] == true,
      storageStatsAvailable: json['storage_stats_available'] == true,
      diskStatsAvailable: json['disk_stats_available'] == true,
      totalFiles: _optionalInt(json['total_files']),
      totalSize: _optionalInt(json['total_size']),
      uniqueSize: _optionalInt(json['unique_size']),
      dedupRatio: _optionalDouble(json['dedup_ratio']),
      diskTotal: _optionalInt(json['disk_total']),
      diskUsed: _optionalInt(json['disk_used']),
      diskAvailable: _optionalInt(json['disk_available']),
      diskUsageRatio: _optionalDouble(json['disk_usage_ratio']),
    );
  }

  final bool totalFilesAvailable;
  final bool storageStatsAvailable;
  final bool diskStatsAvailable;
  final int? totalFiles;
  final int? totalSize;
  final int? uniqueSize;
  final double? dedupRatio;
  final int? diskTotal;
  final int? diskUsed;
  final int? diskAvailable;
  final double? diskUsageRatio;
}

Map<String, dynamic> requireJsonMap(Object? value) {
  if (value is Map<String, dynamic>) {
    return value;
  }
  if (value is Map) {
    return Map<String, dynamic>.from(value);
  }
  throw const FormatException('Expected a JSON object');
}

Map<String, dynamic>? _optionalMap(Object? value) {
  if (value == null) {
    return null;
  }
  return requireJsonMap(value);
}

String _requiredString(Map<String, dynamic> json, String key) {
  final value = json[key];
  if (value is! String || value.trim().isEmpty) {
    throw FormatException('Missing or invalid $key');
  }
  return value;
}

int? _optionalInt(Object? value) {
  if (value == null) {
    return null;
  }
  if (value is! num || value < 0) {
    throw const FormatException('Invalid non-negative integer');
  }
  return value.toInt();
}

double? _optionalDouble(Object? value) {
  if (value == null) {
    return null;
  }
  if (value is! num || !value.isFinite) {
    throw const FormatException('Invalid number');
  }
  return value.toDouble();
}
