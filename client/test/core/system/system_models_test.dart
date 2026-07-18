import 'package:flutter_test/flutter_test.dart';
import 'package:mnemonas_client/core/system/system_models.dart';

void main() {
  group('HealthStatus', () {
    test('accepts degraded health as a valid server response', () {
      final status = HealthStatus.fromJson({
        'status': 'degraded',
        'timestamp': '2026-07-18T12:00:00Z',
        'uptime_secs': 42,
        'version': 'dev',
        'dataplane': {'healthy': false},
      });

      expect(status.isHealthy, isFalse);
      expect(status.dataplaneHealthy, isFalse);
    });

    test('rejects an unknown status', () {
      expect(
        () => HealthStatus.fromJson({
          'status': 'ok',
          'timestamp': '2026-07-18T12:00:00Z',
          'uptime_secs': 42,
          'version': 'dev',
        }),
        throwsFormatException,
      );
    });
  });

  test('ServerVersion rejects a different service', () {
    expect(
      () => ServerVersion.fromJson({
        'name': 'OtherNAS',
        'version': '1',
        'build_time': 'now',
        'go': 'go1.25',
      }),
      throwsFormatException,
    );
  });

  test('StorageStats preserves unavailable values as null', () {
    final stats = StorageStats.fromJson({
      'total_files_available': false,
      'storage_stats_available': false,
      'disk_stats_available': false,
    });

    expect(stats.totalFiles, isNull);
    expect(stats.diskTotal, isNull);
    expect(stats.diskUsageRatio, isNull);
  });
}
