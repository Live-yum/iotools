package io.github.liveyum.iotools;

import android.database.Cursor;
import android.database.MatrixCursor;
import android.os.CancellationSignal;
import android.os.ParcelFileDescriptor;
import android.provider.DocumentsContract;
import android.provider.DocumentsProvider;
import java.io.*;
import java.util.UUID;

/** Disposable SAF fixture packaged only in the instrumentation APK. */
public final class FileFixtureProvider extends DocumentsProvider {
    static final String AUTHORITY="io.github.liveyum.iotools.test.filefixture";
    private File directory;
    @Override public boolean onCreate(){directory=new File(getContext().getCacheDir(),"document-fixtures");return directory.isDirectory()||directory.mkdirs();}
    private File file(String id)throws FileNotFoundException{if(!id.matches("[A-Za-z0-9_.-]+"))throw new FileNotFoundException("invalid fixture ID");return new File(directory,id);}
    @Override public Cursor queryRoots(String[] projection){String[] columns=projection==null?new String[]{DocumentsContract.Root.COLUMN_ROOT_ID,DocumentsContract.Root.COLUMN_DOCUMENT_ID,DocumentsContract.Root.COLUMN_TITLE,DocumentsContract.Root.COLUMN_FLAGS}:projection;MatrixCursor cursor=new MatrixCursor(columns);MatrixCursor.RowBuilder row=cursor.newRow();for(String column:columns){Object value=null;if(column.equals(DocumentsContract.Root.COLUMN_ROOT_ID))value="fixture";else if(column.equals(DocumentsContract.Root.COLUMN_DOCUMENT_ID))value="root";else if(column.equals(DocumentsContract.Root.COLUMN_TITLE))value="iotools disposable test files";else if(column.equals(DocumentsContract.Root.COLUMN_FLAGS))value=DocumentsContract.Root.FLAG_SUPPORTS_CREATE;row.add(value);}return cursor;}
    @Override public Cursor queryDocument(String id,String[] projection)throws FileNotFoundException{String[] columns=projection==null?new String[]{DocumentsContract.Document.COLUMN_DOCUMENT_ID,DocumentsContract.Document.COLUMN_DISPLAY_NAME,DocumentsContract.Document.COLUMN_MIME_TYPE,DocumentsContract.Document.COLUMN_FLAGS,DocumentsContract.Document.COLUMN_SIZE}:projection;MatrixCursor cursor=new MatrixCursor(columns);boolean source=id.startsWith("source-");File file=file(id);if(!source&&!id.equals("root")&&!file.exists())return cursor;MatrixCursor.RowBuilder row=cursor.newRow();for(String column:columns){Object value=null;if(column.equals(DocumentsContract.Document.COLUMN_DOCUMENT_ID))value=id;else if(column.equals(DocumentsContract.Document.COLUMN_DISPLAY_NAME))value=id.equals("source-name")?"../../秘密\\payload.bin":id;else if(column.equals(DocumentsContract.Document.COLUMN_MIME_TYPE))value=id.equals("root")?DocumentsContract.Document.MIME_TYPE_DIR:"application/octet-stream";else if(column.equals(DocumentsContract.Document.COLUMN_FLAGS))value=DocumentsContract.Document.FLAG_SUPPORTS_WRITE|DocumentsContract.Document.FLAG_SUPPORTS_DELETE;else if(column.equals(DocumentsContract.Document.COLUMN_SIZE)){if(id.equals("source-declared-large"))value=4096L;else if(id.equals("source-unknown")||id.equals("source-oversize"))value=null;else if(source)value=64L;else value=file.length();}row.add(value);}return cursor;}
    @Override public Cursor queryChildDocuments(String parent,String[] projection,String sortOrder){return new MatrixCursor(projection==null?new String[]{DocumentsContract.Document.COLUMN_DOCUMENT_ID}:projection);}
    @Override public String createDocument(String parent,String mime,String displayName)throws FileNotFoundException{String id=(displayName.startsWith("undeletable")?"undeletable-":"export-")+UUID.randomUUID();try{if(!file(id).createNewFile())throw new IOException("duplicate fixture");return id;}catch(IOException error){throw new FileNotFoundException(error.getMessage());}}
    @Override public ParcelFileDescriptor openDocument(String id,String mode,CancellationSignal signal)throws FileNotFoundException{File file=file(id);if(id.equals("source-declared-large"))throw new FileNotFoundException("declared-large fixture must be rejected before open");if(id.startsWith("source-")&&!file.exists()){try(OutputStream output=new FileOutputStream(file)){int count=id.equals("source-oversize")?512:64;for(int i=0;i<count;i++)output.write(i&255);}catch(IOException error){throw new FileNotFoundException(error.getMessage());}}return ParcelFileDescriptor.open(file,ParcelFileDescriptor.parseMode(mode));}
    @Override public void deleteDocument(String id)throws FileNotFoundException{if(id.startsWith("undeletable-"))throw new FileNotFoundException("fixture provider does not support deleting this document");File file=file(id);if(file.exists()&&!file.delete())throw new FileNotFoundException("cannot delete fixture");}
}
