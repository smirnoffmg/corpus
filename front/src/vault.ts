import { createContext } from 'react'

// VaultContext holds the Obsidian vault's name as the server reports it; notes
// can only be linked to once it is known.
export const VaultContext = createContext<string | undefined>(undefined)
