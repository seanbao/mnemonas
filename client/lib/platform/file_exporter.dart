import 'dart:io';

import 'package:file_selector/file_selector.dart';
import 'package:flutter/services.dart';

final class FileExportTarget {
  const FileExportTarget._({this.path, this.contentUri});

  factory FileExportTarget.path(String path) => FileExportTarget._(path: path);

  factory FileExportTarget.contentUri(String uri) =>
      FileExportTarget._(contentUri: uri);

  final String? path;
  final String? contentUri;

  bool get requiresPrivateStaging => contentUri != null;
}

abstract final class FileExporter {
  static const MethodChannel _channel = MethodChannel(
    'com.mnemonas.app/file_export',
  );

  static Future<FileExportTarget?> chooseTarget({
    required String suggestedName,
    required String mimeType,
  }) async {
    if (Platform.isAndroid) {
      final uri = await _channel.invokeMethod<String>('createDocument', {
        'suggestedName': suggestedName,
        'mimeType': mimeType,
      });
      return uri == null ? null : FileExportTarget.contentUri(uri);
    }

    final location = await getSaveLocation(suggestedName: suggestedName);
    return location == null ? null : FileExportTarget.path(location.path);
  }

  static Future<void> exportStagedFile({
    required String sourcePath,
    required FileExportTarget target,
  }) async {
    final uri = target.contentUri;
    if (!Platform.isAndroid || uri == null) {
      throw StateError('The export target does not require native staging');
    }
    await _channel.invokeMethod<void>('copyToDocument', {
      'sourcePath': sourcePath,
      'targetUri': uri,
    });
  }
}
