import 'package:flutter_test/flutter_test.dart';
import 'package:mnemonas_client/core/files/file_models.dart';

void main() {
  group('directory listing paths', () {
    test('accepts a canonical path matching the request', () {
      final listing = DirectoryListing.fromJson(
        _listingJson('/文档'),
        expectedPath: '/文档',
      );

      expect(listing.path, '/文档');
    });

    test('rejects non-canonical response paths', () {
      for (final path in <String>[
        '文档',
        '/文档/',
        '/文档//项目',
        '/文档/../私密',
        r'/文档\私密',
      ]) {
        expect(
          () => DirectoryListing.fromJson(_listingJson(path)),
          throwsFormatException,
          reason: path,
        );
      }
    });

    test('rejects a canonical path for a different request', () {
      expect(
        () =>
            DirectoryListing.fromJson(_listingJson('/其他'), expectedPath: '/文档'),
        throwsFormatException,
      );
    });
  });
}

Map<String, Object?> _listingJson(String path) {
  return <String, Object?>{
    'path': path,
    'capabilities': <String, Object?>{
      'read': true,
      'concreteRead': true,
      'write': true,
    },
    'deleteMode': 'trash',
    'deletePolicyToken': List<String>.filled(64, 'a').join(),
    'trashRetentionDays': 30,
    'trashAutoCleanupEnabled': true,
    'files': <Object?>[],
  };
}
