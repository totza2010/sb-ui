// @cubone/react-file-manager ships no type declarations — minimal ambient types
// for the props we use. (Vite loads the built ESM from the package's `module`
// field; this only satisfies the TS compiler.)
declare module '@cubone/react-file-manager' {
  import type { ComponentType } from 'react'

  export interface CuboneFile {
    name: string
    isDirectory: boolean
    path: string
    size?: number
    updatedAt?: string
  }

  export interface FileManagerProps {
    files: CuboneFile[]
    initialPath?: string
    isLoading?: boolean
    height?: number | string
    onFolderChange?: (path: string) => void
    onRefresh?: () => void
    onSelect?: (files: CuboneFile[]) => void
    onSelectionChange?: (files: CuboneFile[]) => void
    onCreateFolder?: (name: string, parentFolder: CuboneFile) => void
    onRename?: (file: CuboneFile, newName: string) => void
    onDelete?: (files: CuboneFile[]) => void
    onPaste?: (files: CuboneFile[], destination: CuboneFile, operationType: 'copy' | 'move') => void
    onDownload?: (files: CuboneFile[]) => void
    fileUploadConfig?: { url: string; method?: string; headers?: Record<string, string> }
    onFileUploading?: (file: unknown, parentFolder?: CuboneFile) => Record<string, string> | void
    onFileUploaded?: (response: unknown) => void
    // allow any other props the library supports
    [key: string]: unknown
  }

  export const FileManager: ComponentType<FileManagerProps>
}
